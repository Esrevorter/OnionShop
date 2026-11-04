package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Phase 3 auth handlers (TASKS.md): registration + login built on the
// service layer shipped earlier (password.go, captcha.go, sessions.go).
// Constraints that must not regress:
//   - every POST form carries CSRF + image CAPTCHA + honeypot (AGENTS.md §3);
//   - session rotates on login and on privilege change (AGENTS.md §4);
//   - no PII ever logged — usernames are NOT a secret but stay out of logs
//     anyway; failures report generic shapes (SECURITY_AND_OPSEC.md §1);
//   - pgx only, context on every query (AGENTS.md §2).

// honeypotField is the decoy input name. Hidden via CSS only (no JS exists);
// any browser-agnostic bot fills it in and gets silently rejected.
const honeypotField = "sta"

// AuthData renders the login/register/account/pgp templates. The Phase 4
// slices back the vendor/admin pages (vendor_products, admin_vendors); they
// stay nil on every other page, where the templates never touch them.
type AuthData struct {
	CsrfToken      string
	Captcha        *CaptchaChallenge
	HoneypotHidden bool
	Error          string
	Flash          string
	PersonalPhrase string // anti-phishing phrase shown ONLY after login (§9)
	PGPPublicKey   string // armored PUBLIC key only (Phase 3 PGP); never private material
	PGPFingerprint string // display fingerprint of PGPPublicKey ("" when absent)

	Products    []ProductRecord  // vendor dashboard rows (vendor_handlers.go)
	Categories  []CategoryRecord // category dropdown options
	Pending     []VendorRecord   // admin approval queue (handleAdminVendors)
	VendorState *VendorRecord    // this account's vendor row, if any
}

// UserRecord is one row from public.users. PasswordHash never leaves this
// package's verify path and is never rendered.
type UserRecord struct {
	ID           pgtype.UUID
	Username     string
	PasswordHash string
	Role         string
	Status       string
	CreatedAt    time.Time
}

// SessionInvalidator kills every live session belonging to one user. The
// production implementation is store.FallbackStore (Redis fast path +
// Postgres durable table); handlers take the narrow interface so tests can
// observe invalidation without either backend. It MUST be wired in production:
// role changes and account suspensions are meaningless as long as old session
// rows keep authenticating requests, and scs' gob payload cannot be edited
// server-side — deletion is the only correct primitive.
type SessionInvalidator interface {
	InvalidateUserSessions(ctx context.Context, userID string) error
}

// AuthStore is the durable half of authentication. *pgxpool.Pool satisfies it
// directly; tests substitute an in-memory fake so the full handler chain runs
// without a database (the CI lane has no Postgres service).
type AuthStore interface {
	AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error)
	AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error)
	AuthUpdatePasswordHash(ctx context.Context, id pgtype.UUID, passwordHash string) error
	AuthSetPersonalPhrase(ctx context.Context, id pgtype.UUID, phrase string) error
	AuthRecordLogin(ctx context.Context, userID pgtype.UUID, at time.Time, host string) error
	// Phase 3 PGP: armored PUBLIC keys only. Implementations must never accept
	// or store private material — validation happens before these are called
	// (pgp.go), and the DB column contract is identical (migration 0001).
	AuthGetPGPKey(ctx context.Context, id pgtype.UUID) (string, error)
	AuthSetPGPKey(ctx context.Context, id pgtype.UUID, armoredPublicKey string) error
	AuthClearPGPKey(ctx context.Context, id pgtype.UUID) error
	// Phase 4 vendor flow: vendors-table rows behind approval. The handler
	// layer enforces role/status gates BEFORE calling these; implementations
	// stay thin SQL adapters.
	VendorCreateRequest(ctx context.Context, userID pgtype.UUID, shopName string) error
	VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error)
	VendorListPending(ctx context.Context) ([]VendorRecord, error)
	// VendorGetByID resolves one vendors row by its own id (admin approval).
	// Unknown ids return (nil, nil) — never an error — so handlers treat
	// missing rows uniformly.
	VendorGetByID(ctx context.Context, vendorID pgtype.UUID) (*VendorRecord, error)
	// VendorByShopName resolves the UNIQUE shop_name (case-insensitive).
	// Unknown names return (nil, nil).
	VendorByShopName(ctx context.Context, shopName string) (*VendorRecord, error)
	VendorSetApproval(ctx context.Context, vendorID pgtype.UUID, approved bool) error
	VendorSetRoleToVendor(ctx context.Context, userID pgtype.UUID) error
	// VendorUsername resolves a pending/approved vendor row's owner to the
	// canonical username for audit-log details (never raw PII beyond the
	// display identity the action is about). Unknown ids return "".
	VendorUsername(ctx context.Context, vendorUserID pgtype.UUID) (string, error)
	// AuthUserRole re-reads the authoritative role for one user id. Login
	// compares it against the role embedded in the session and kills stale
	// sessions on mismatch (AGENTS.md §4 — privilege changes must never ride
	// an old cookie). Unknown ids return ErrNoSuchUser.
	AuthUserRole(ctx context.Context, id pgtype.UUID) (string, error)
	// AuthUserByID fetches the full authoritative USERS row by primary key
	// (vendor request flow re-checks role/status server-side). Unknown ids
	// return ErrNoSuchUser.
	AuthUserByID(ctx context.Context, id pgtype.UUID) (*UserRecord, error)
	// AuditAppend writes one immutable audit_log row (migration 0003).
	// action is a stable verb ('vendor.approve', 'product.create', ...),
	// target is '<table>:<id>', details is pre-sanitized JSON text ("" stores
	// '{}'). NO secrets/PII may ever reach details (SECURITY_AND_OPSEC.md §3).
	AuditAppend(ctx context.Context, actorID pgtype.UUID, actorKind, action, target, details string) error
}

// SessionLinker is the optional interface the durable session store satisfies
// (store.SessionFallback / store.FallbackStore). Login calls it to stamp
// sessions.user_id so per-user session invalidation (role changes, bans) can
// find the rows later. Absent linker = degraded dev mode: sessions still work,
// admin-kill simply has nothing to match on.
type SessionLinker interface {
	LinkUser(ctx context.Context, token, userID string) error
}

// ErrNoSuchUser is the sentinel for unknown usernames; handlers treat it like
// a bad password (identical timing, identical message).
var ErrNoSuchUser = errors.New("store: no such user")

// ---- pgx-backed implementation of AuthStore -------------------------------

// authQueries mirror db/query/users.sql plus the Phase 3 additions in
// migration 0006. Keep both in sync when columns move.
const (
	qAuthUserByUsername = `SELECT id, username, password_hash, role, status, created_at
FROM public.users WHERE username = $1`

	qAuthCreateUser = `INSERT INTO public.users (username, password_hash, role, status)
VALUES ($1, $2, 'customer', $3)
RETURNING id, username, password_hash, role, status, created_at`

	qAuthUpdatePasswordHash = `UPDATE public.users SET password_hash = $2, updated_at = now() WHERE id = $1`

	qAuthSetPhrase = `UPDATE public.users SET personal_phrase = $2, updated_at = now() WHERE id = $1`

	qAuthRecordLogin = `INSERT INTO public.login_history (user_id, occurred_at, host_header) VALUES ($1, $2, $3)`

	// Phase 3 PGP (pgp.go): armored PUBLIC key only; private keys are rejected
	// by validation long before these run. Mirrors UpdateUserPGPKey /
	// ClearUserPGPKey in db/query/users.sql — keep both in sync.
	qAuthGetPGPKey = `SELECT COALESCE(pgp_public_key, '') FROM public.users WHERE id = $1`

	qAuthSetPGPKey = `UPDATE public.users SET pgp_public_key = $2, updated_at = now() WHERE id = $1`

	qAuthClearPGPKey = `UPDATE public.users SET pgp_public_key = NULL, updated_at = now() WHERE id = $1`
)

// PGAuthStore adapts *pgxpool.Pool to AuthStore.
type PGAuthStore struct{ pool *pgxpool.Pool }

// NewPGAuthStore wraps the production pool.
func NewPGAuthStore(pool *pgxpool.Pool) *PGAuthStore { return &PGAuthStore{pool: pool} }

func scanUser(row pgx.Row) (*UserRecord, error) {
	u := &UserRecord{}
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status, &u.CreatedAt); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *PGAuthStore) AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthUserByUsername, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSuchUser
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user: %w", err)
	}
	return u, nil
}

func (s *PGAuthStore) AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthCreateUser, username, passwordHash, status))
	if err != nil {
		// Unique-violation (23xxx) comes back as an error; handlers map it to
		// the generic "username unavailable" message without echoing the DB.
		return nil, fmt.Errorf("store: create user: %w", err)
	}
	return u, nil
}

func (s *PGAuthStore) AuthUpdatePasswordHash(ctx context.Context, id pgtype.UUID, passwordHash string) error {
	if _, err := s.pool.Exec(ctx, qAuthUpdatePasswordHash, id, passwordHash); err != nil {
		return fmt.Errorf("store: update password hash: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthSetPersonalPhrase(ctx context.Context, id pgtype.UUID, phrase string) error {
	if _, err := s.pool.Exec(ctx, qAuthSetPhrase, id, phrase); err != nil {
		return fmt.Errorf("store: set personal phrase: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthRecordLogin(ctx context.Context, userID pgtype.UUID, at time.Time, host string) error {
	if _, err := s.pool.Exec(ctx, qAuthRecordLogin, userID, at.UTC(), host); err != nil {
		return fmt.Errorf("store: record login: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthGetPGPKey(ctx context.Context, id pgtype.UUID) (string, error) {
	var key string
	if err := s.pool.QueryRow(ctx, qAuthGetPGPKey, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoSuchUser
		}
		return "", fmt.Errorf("store: get pgp key: %w", err)
	}
	return key, nil
}

func (s *PGAuthStore) AuthSetPGPKey(ctx context.Context, id pgtype.UUID, armoredPublicKey string) error {
	if _, err := s.pool.Exec(ctx, qAuthSetPGPKey, id, armoredPublicKey); err != nil {
		return fmt.Errorf("store: set pgp key: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthClearPGPKey(ctx context.Context, id pgtype.UUID) error {
	if _, err := s.pool.Exec(ctx, qAuthClearPGPKey, id); err != nil {
		return fmt.Errorf("store: clear pgp key: %w", err)
	}
	return nil
}

// ---- Phase 4 vendor + audit implementation ---------------------------------

const (
	qVendorCreate = `INSERT INTO public.vendors (user_id, shop_name, approved)
VALUES ($1, $2, FALSE)`

	qVendorByUser = `SELECT id, user_id, shop_name, approved, created_at
FROM public.vendors WHERE user_id = $1`

	qVendorPending = `SELECT v.id, v.user_id, v.shop_name, v.approved, v.created_at
FROM public.vendors v JOIN public.users u ON u.id = v.user_id
WHERE NOT v.approved AND u.role = 'customer'
ORDER BY v.created_at ASC LIMIT 200`

	qVendorSetApproval = `UPDATE public.vendors SET approved = $2 WHERE id = $1`

	// Audit-detail lookup: the username of a vendor-row owner. Display
	// identity only (usernames are not secrets, AGENTS.md §4) — never the
	// password hash or any other column rides this query.
	qVendorUsername = `SELECT username FROM public.users WHERE id = $1`

	// Authoritative role read used by login to detect sessions whose embedded
	// role went stale (approval/ban between cookie mint and request).
	qAuthUserRole = `SELECT role FROM public.users WHERE id = $1`

	// Promotion only ever moves customer -> vendor; a mod/admin row must never
	// be demoted by this path (the CHECK set stays intact either way, but the
	// WHERE clause makes accidental misuse impossible).
	qVendorPromoteRole = `UPDATE public.users SET role = 'vendor', updated_at = now()
WHERE id = $1 AND role = 'customer'`

	// ---- Phase 4 catalog/vendor queries (mirror db/query/products.sql) ----

	qVendorByID = `SELECT id, user_id, shop_name, approved, created_at
FROM public.vendors WHERE id = $1`

	qVendorByShopName = `SELECT id, user_id, lower(shop_name), approved, created_at
FROM public.vendors WHERE lower(shop_name) = $1`

	qCategoryList = `SELECT id, slug, name FROM public.categories ORDER BY name ASC LIMIT 200`

	qProductInsert = `INSERT INTO public.products
(vendor_id, category_id, title, description, price_sat, stock, image_path, status)
VALUES ($1, NULLIF($2, 0), $3, $4, $5, $6, NULLIF($7, ''), $8)
RETURNING id`

	qProductGet = `SELECT id, vendor_id, COALESCE(category_id, 0), title, description,
price_sat, stock, COALESCE(image_path, ''), status
FROM public.products WHERE id = $1`

	qProductUpdate = `UPDATE public.products SET
category_id = NULLIF($2, 0), title = $3, description = $4, price_sat = $5,
stock = $6, image_path = NULLIF($7, ''), status = $8, updated_at = now()
WHERE id = $1`

	qProductSetStatus = `UPDATE public.products SET status = $2, updated_at = now() WHERE id = $1`

	qProductsByVendor = `SELECT id, vendor_id, COALESCE(category_id, 0), title, description,
price_sat, stock, COALESCE(image_path, ''), status
FROM public.products WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT 500`

	qProductsActive = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
p.price_sat, p.stock, COALESCE(p.image_path, ''), p.status
FROM public.products p JOIN public.vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND (NULLIF($1, 0::bigint) IS NULL OR p.category_id = NULLIF($1, 0::bigint))
ORDER BY p.created_at DESC LIMIT $2`

	qProductsSearch = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
p.price_sat, p.stock, COALESCE(p.image_path, ''), p.status
FROM public.products p JOIN public.vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND to_tsvector('simple', p.title || ' ' || p.description) @@ plainto_tsquery('simple', $1)
ORDER BY ts_rank(to_tsvector('simple', p.title || ' ' || p.description),
                 plainto_tsquery('simple', $1)) DESC LIMIT $2`

	qShopNameForID = `SELECT shop_name FROM public.vendors WHERE id = $1 AND approved`

	qApprovedVendorID = `SELECT id FROM public.vendors WHERE user_id = $1 AND approved`

	qAuditAppend = `INSERT INTO public.audit_log (actor_id, actor_kind, action, target, details)
VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, '')::jsonb, '{}'::jsonb))`
)

// VendorRecord is one row from public.vendors.
type VendorRecord struct {
	ID        pgtype.UUID
	UserID    pgtype.UUID
	ShopName  string
	Approved  bool
	CreatedAt time.Time
}

func scanVendor(row pgx.Row) (VendorRecord, error) {
	var v VendorRecord
	err := row.Scan(&v.ID, &v.UserID, &v.ShopName, &v.Approved, &v.CreatedAt)
	return v, err
}

func (s *PGAuthStore) VendorCreateRequest(ctx context.Context, userID pgtype.UUID, shopName string) error {
	if _, err := s.pool.Exec(ctx, qVendorCreate, userID, shopName); err != nil {
		return fmt.Errorf("store: create vendor request: %w", err)
	}
	return nil
}

func (s *PGAuthStore) VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByUser, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // no vendor row yet — distinct from a store failure
	}
	if err != nil {
		return nil, fmt.Errorf("store: get vendor: %w", err)
	}
	return &v, nil
}

func (s *PGAuthStore) VendorListPending(ctx context.Context) ([]VendorRecord, error) {
	rows, err := s.pool.Query(ctx, qVendorPending)
	if err != nil {
		return nil, fmt.Errorf("store: list pending vendors: %w", err)
	}
	defer rows.Close()
	out := []VendorRecord{}
	for rows.Next() {
		v, err := scanVendor(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list pending vendors: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pending vendors: %w", err)
	}
	return out, nil
}

// VendorGetByID resolves one vendors row by primary key; unknown ids yield
// (nil, nil) so the admin approve handler can redirect uniformly instead of
// distinguishing "gone" from "never existed".
func (s *PGAuthStore) VendorGetByID(ctx context.Context, vendorID pgtype.UUID) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByID, vendorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get vendor by id: %w", err)
	}
	return &v, nil
}

// VendorByShopName resolves the UNIQUE shop_name case-insensitively (handles
// are lowercase on the wire; the schema stores what was applied for). The
// returned record carries the LOWER-cased name — the canonical handle.
func (s *PGAuthStore) VendorByShopName(ctx context.Context, shopName string) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByShopName, strings.ToLower(shopName)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get vendor by shop name: %w", err)
	}
	return &v, nil
}

// ---- Phase 4 catalog: PGAuthStore doubles as VendorStore --------------------

func (s *PGAuthStore) CategoryList(ctx context.Context) ([]CategoryRecord, error) {
	rows, err := s.pool.Query(ctx, qCategoryList)
	if err != nil {
		return nil, fmt.Errorf("store: category list: %w", err)
	}
	defer rows.Close()
	out := []CategoryRecord{}
	for rows.Next() {
		var c CategoryRecord
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name); err != nil {
			return nil, fmt.Errorf("store: category list: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: category list: %w", err)
	}
	return out, nil
}

func scanProduct(row pgx.Row) (ProductRecord, error) {
	var p ProductRecord
	err := row.Scan(&p.ID, &p.VendorID, &p.CategoryID, &p.Title, &p.Description,
		&p.PriceSat, &p.Stock, &p.ImagePath, &p.Status)
	return p, err
}

func (s *PGAuthStore) ProductCreate(ctx context.Context, p *ProductRecord) error {
	return s.pool.QueryRow(ctx, qProductInsert, p.VendorID, p.CategoryID, p.Title,
		p.Description, p.PriceSat, p.Stock, p.ImagePath, p.Status).Scan(&p.ID)
}

func (s *PGAuthStore) ProductGet(ctx context.Context, id pgtype.UUID) (*ProductRecord, error) {
	p, err := scanProduct(s.pool.QueryRow(ctx, qProductGet, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSuchUser // sentinel: handlers map it to "no such product"
	}
	if err != nil {
		return nil, fmt.Errorf("store: get product: %w", err)
	}
	return &p, nil
}

func (s *PGAuthStore) ProductUpdate(ctx context.Context, p *ProductRecord) error {
	_, err := s.pool.Exec(ctx, qProductUpdate, p.ID, p.CategoryID, p.Title, p.Description,
		p.PriceSat, p.Stock, p.ImagePath, p.Status)
	if err != nil {
		return fmt.Errorf("store: update product: %w", err)
	}
	return nil
}

func (s *PGAuthStore) ProductSetStatus(ctx context.Context, id pgtype.UUID, status string) error {
	if _, err := s.pool.Exec(ctx, qProductSetStatus, id, status); err != nil {
		return fmt.Errorf("store: set product status: %w", err)
	}
	return nil
}

func (s *PGAuthStore) queryProducts(ctx context.Context, sql string, args ...any) ([]ProductRecord, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("store: product query: %w", err)
	}
	defer rows.Close()
	out := []ProductRecord{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("store: product query: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: product query: %w", err)
	}
	return out, nil
}

func (s *PGAuthStore) ProductListByVendor(ctx context.Context, vendorID pgtype.UUID) ([]ProductRecord, error) {
	return s.queryProducts(ctx, qProductsByVendor, vendorID)
}

func (s *PGAuthStore) ProductListActive(ctx context.Context, categoryID int64, limit int) ([]ProductRecord, error) {
	return s.queryProducts(ctx, qProductsActive, categoryID, limit)
}

func (s *PGAuthStore) ProductSearch(ctx context.Context, query string, limit int) ([]ProductRecord, error) {
	return s.queryProducts(ctx, qProductsSearch, query, limit)
}

func (s *PGAuthStore) ShopNameForID(ctx context.Context, vendorID pgtype.UUID) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, qShopNameForID, vendorID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil // unapproved/unknown shop renders without a name, never an error page
	}
	if err != nil {
		return "", fmt.Errorf("store: shop name: %w", err)
	}
	return name, nil
}

func (s *PGAuthStore) ApprovedVendorID(ctx context.Context, userID pgtype.UUID) (*pgtype.UUID, error) {
	var id pgtype.UUID
	err := s.pool.QueryRow(ctx, qApprovedVendorID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: approved vendor id: %w", err)
	}
	return &id, nil
}

func (s *PGAuthStore) VendorSetApproval(ctx context.Context, vendorID pgtype.UUID, approved bool) error {
	if _, err := s.pool.Exec(ctx, qVendorSetApproval, vendorID, approved); err != nil {
		return fmt.Errorf("store: set vendor approval: %w", err)
	}
	return nil
}

func (s *PGAuthStore) VendorSetRoleToVendor(ctx context.Context, userID pgtype.UUID) error {
	if _, err := s.pool.Exec(ctx, qVendorPromoteRole, userID); err != nil {
		return fmt.Errorf("store: promote role: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuditAppend(ctx context.Context, actorID pgtype.UUID, actorKind, action, target, details string) error {
	if actorKind == "" {
		actorKind = "user"
	}
	if _, err := s.pool.Exec(ctx, qAuditAppend, actorID, actorKind, action, target, details); err != nil {
		return fmt.Errorf("store: audit append: %w", err)
	}
	return nil
}

// VendorUsername returns the owner's canonical username for audit details;
// unknown ids yield "" without an error so a vanished user can never wedge
// the approval flow over bookkeeping.
func (s *PGAuthStore) VendorUsername(ctx context.Context, vendorUserID pgtype.UUID) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, qVendorUsername, vendorUserID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: vendor username: %w", err)
	}
	return name, nil
}

// AuthUserRole reads the authoritative role; ErrNoSuchUser for unknown ids.
func (s *PGAuthStore) AuthUserRole(ctx context.Context, id pgtype.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, qAuthUserRole, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoSuchUser
	}
	if err != nil {
		return "", fmt.Errorf("store: get role: %w", err)
	}
	return role, nil
}

// ---- wiring ---------------------------------------------------------------

// RegisterAuthRoutes mounts the Phase 3 auth surface on the app router. Call
// it AFTER New() so the middleware stack (sessions → CSRF → rate limit) is
// already in place. Nil guards keep dev boots honest instead of panicking
// mid-request. templateDir is parsed eagerly: a boot with unparseable or
// missing templates fails loudly here rather than answering every page
// request with a generic 500 later. sessions may be nil (dev/in-memory mode):
// login then skips the durable user_id stamp and admin-side session kill has
// nothing to match on — logged once, never silent.
func (a *App) RegisterAuthRoutes(auth AuthStore, captcha *CaptchaService, sessions SessionLinker, templateDir string) error {
	return a.RegisterAuthRoutesFull(auth, captcha, sessions, nil, nil, templateDir)
}

// RegisterAuthRoutesFull is the Phase 4 wiring entry point: the same auth
// surface plus the durable session invalidator (kills every live session of a
// user whose role changed — vendor approval, bans) and the product catalog
// store behind the storefront pages. Both extras are optional; nil degrades
// loudly exactly like the linker above (dev/in-memory mode). The invalidator
// doubles as the linker when no separate linker was passed (production passes
// the same store.FallbackStore value for both).
func (a *App) RegisterAuthRoutesFull(auth AuthStore, captcha *CaptchaService, sessions SessionLinker, invalid SessionInvalidator, catalog VendorStore, templateDir string) error {
	if err := a.buildTemplates(templateDir); err != nil {
		return err
	}
	if invalid == nil && sessions != nil {
		// One backend, two roles: store.FallbackStore satisfies both. An
		// explicit nil stays loud below at each use site instead.
		if si, ok := sessions.(SessionInvalidator); ok {
			invalid = si
		}
	}
	a.invalid = invalid
	a.vendor = catalog
	if auth == nil || captcha == nil {
		log.Print("server: auth routes not registered (missing store or captcha service)")
		return nil
	}
	a.auth = auth
	a.captcha = captcha
	a.linker = sessions
	r := a.router
	r.Get("/", a.handleHome)
	r.Get("/register", a.handleRegisterForm)
	r.Post("/register", a.handleRegisterSubmit)
	r.Get("/login", a.handleLoginForm)
	r.Post("/login", a.handleLoginSubmit)
	r.Post("/logout", a.handleLogout)
	// RequireRole is middleware: func(http.Handler) http.Handler. chi's
	// Get/Post take an http.HandlerFunc, so the gated handler goes through
	// Method (which accepts a full http.Handler).
	accountMW := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/account", accountMW(http.HandlerFunc(a.handleAccount)))
	r.Method(http.MethodPost, "/account/phrase", accountMW(http.HandlerFunc(a.handlePhraseSubmit)))
	// Phase 3 PGP surface (pgp.go): public-key upload/clear behind the same
	// account gate + friction, and the public /pgp explainer page.
	r.Method(http.MethodPost, "/account/pgp", accountMW(http.HandlerFunc(a.handlePGPUpload)))
	r.Method(http.MethodPost, "/account/pgp/clear", accountMW(http.HandlerFunc(a.handlePGPClear)))
	r.Get("/pgp", a.handlePGPToolPage)
	// Phase 4 vendor registration + admin approval (vendor_handlers.go),
	// mounted through the SAME gate/friction pattern as the account surface.
	a.registerVendorRoutes(r)
	// Phase 4 storefront: public catalog browsing (catalog_handlers.go).
	a.registerCatalogRoutes(r)
	return nil
}

// handleHome is the minimal Phase 3 landing page; the catalog replaces it in
// Phase 4. It stays trivially renderable so router tests can hit "/".
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.Flash = ""
	a.render(w, "home", http.StatusOK, data)
}

// handleCaptchaImage serves one issued puzzle as PNG from server-side state
// (never client-supplied). Unknown/expired ids answer with a fresh render of
// the same deterministic image path — actually: a plain 404, because a
// different-looking error image would confirm to a scraper which ids were
// real. The id comes from an HMAC-derived namespace, so guessing one is
// infeasible anyway; this just avoids leaking existence across expiry.
func (a *App) handleCaptchaImage(w http.ResponseWriter, r *http.Request) {
	if a.captcha == nil {
		writeGenericError(w)
		return
	}
	id := chi.URLParam(r, "id")
	ch, ok := a.captcha.Challenge(r.Context(), id)
	if !ok || len(ch.Image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ch.Image)
}

// ---- form friction helpers -------------------------------------------------

// honeypotTriggered reports whether the bot-only field was filled. The request
// is answered with a benign-looking form re-render (never an explicit refusal)
// so scrapers learn nothing about the detector.
func honeypotTriggered(r *http.Request) bool {
	return strings.TrimSpace(r.FormValue(honeypotField)) != ""
}

// requireFriction validates CAPTCHA answer + honeypot for one POST. ok=false
// means the caller must re-render the form with a fresh challenge; reason is
// safe to show (it never reveals honeypot presence). purpose is checked at
// issue time and bound into the token MAC by the CAPTCHA service, so replay
// of a register challenge against the login form fails verification here.
func (a *App) requireFriction(w http.ResponseWriter, r *http.Request, purpose string) (ok bool) {
	ctx := r.Context()
	// The challenge was minted against the token THIS request carries — i.e.
	// the cookie value, not whatever LoadAndSave generated mid-flight for a
	// session that only became non-empty on this very response. On anonymous
	// form posts (login/register) sm.Token(ctx) is still "" here because scs
	// commits the fresh token at response time; reading it verbatim would
	// make every first-post CAPTCHA fail its binding MAC. Prefer the raw
	// cookie; fall back to the committed ctx token when no cookie is present.
	token := a.sessionTokenForRequest(r)
	userID := CurrentUserID(ctx, a.sm)
	id := r.FormValue("captcha_id")
	ans := r.FormValue("captcha_answer")
	if err := a.captcha.Verify(ctx, r.FormValue("captcha_token"), id, ans, token, userID); err != nil {
		return false
	}
	_ = purpose // documented above: binding is enforced inside Verify's MAC check
	// Burn only on success: a wrong answer keeps the challenge alive so the
	// user sees the same puzzle (rate limiting bounds retries), while a
	// correct answer can never be replayed (single-use guarantee).
	a.captcha.Burn(ctx, id)
	// Challenge consumed and the exchange authenticated by possession of the
	// session cookie: drop the predicted-token stash so it never lingers in
	// session data (and never gets copied into a post-login session).
	a.sm.PopString(ctx, pendingTokenKey)
	return true
}

// pendingTokenKey namespaces the ephemeral per-request token stash inside the
// session itself. It lives only while StatusNone/Unchanged sessions get their
// first real write (login success, CSRF mint), and every consumer treats a
// missing entry as "no pending token".
const pendingTokenKey = "pending_token"

// sessionTokenForRequest returns the session token a challenge is bound to.
// scs mints fresh identifiers lazily and commits them only at response time,
// so neither the raw cookie nor sm.Token(ctx) alone is a stable anchor across
// the GET→POST pair of an anonymous visit:
//   - GET /login with no cookie: the browser has nothing yet; scs will mint T1
//     when this response's session data is committed (CSRFLoad/login Put).
//     The ctx token at handler time is still "".
//   - POST /login carrying T1: the cookie IS the token the challenge was
//     issued against — but if the session store lost T1 (memory-store flush,
//     Redis eviction, or any commit race), scs silently re-mints T2 in the
//     context, and a MAC bound to T1 would fail verification forever.
//
// Resolution order therefore prefers the live ctx token (authoritative once
// committed), falls back to the raw cookie (first-post case above), and also
// consults the pending token recorded at issue time when the previous hop had
// no identifier yet. All three agree in the normal browser flow; the extra
// sources only rescue the paths where scs' lazy-commit semantics would
// otherwise break the binding.
func (a *App) sessionTokenForRequest(r *http.Request) string {
	ctx := r.Context()
	pending := a.sm.GetString(ctx, pendingTokenKey)
	if ck, err := r.Cookie(SessionCookieName); err == nil && ck.Value != "" {
		// A pre-login session that already carries its own stashed identifier
		// (minted by an earlier cookie-less GET whose LoadAndSave commit ended
		// up generating a different token than the stash — e.g. the CSRF or
		// absolute-expiry middleware destroyed/re-minted before flushing) must
		// keep verifying against THAT value: challenges issued during this
		// request's render were MAC'd with it. Without this, every retry after
		// a failed login re-anchors to the raw cookie and fails the binding
		// forever, even though the browser solved the fresh puzzle correctly.
		if pending != "" {
			return pending
		}
		return ck.Value
	}
	// Cookie-less request: whatever identifier THIS request has already
	// committed (or will commit verbatim — Commit never re-mints an existing
	// token) is the one the following POST carries in its cookie. Prefer it
	// over a stale stash from an earlier hop; fall back to the stash only when
	// nothing exists yet (the classic first-GET case recordPendingToken fixes).
	if tok := a.sm.Token(ctx); tok != "" {
		return tok
	}
	return pending
}

// rotateToFreshToken commits a brand-new session identifier for THIS response
// and returns it. The caller must mint any challenge bound to the session
// token AFTER this call, so issue-time and verify-time anchors are identical.
//
// Why this exists: scs' lazy-commit semantics make the raw cookie an unstable
// anchor whenever the handler chain rotates the identifier mid-request
// (Destroy + RenewToken in AbsoluteExpiry, RotateOnAuth after login). The
// rotation only reaches the client as a Set-Cookie header at response flush,
// but requireFriction on the following POST reads sm.Token(ctx) — which is
// still "" until the next request's LoadAndSave populates it from the cookie.
// An empty ctx token makes sessionTokenForRequest fall back to the stale raw
// cookie, so every post-rotation form silently fails its CAPTCHA binding MAC
// while the pre-login (pending-token) path keeps working. Re-stashing the
// committed token under pendingTokenKey at every rotation point gives all
// three resolution sources one consistent answer across the GET→POST pair.
//
// The stash is self-healing: requireFriction Pop's it on successful verify,
// recordPendingToken skips it whenever an identifier already exists, and the
// login success path overwrites the session wholesale via rotation below.
func (a *App) rotateToFreshToken(ctx context.Context) error {
	token, _, err := a.sm.Commit(ctx)
	if err != nil {
		return err
	}
	a.sm.Put(ctx, pendingTokenKey, token)
	return nil
}

// recordPendingToken stashes the token that scs is about to commit for this
// response, so a challenge minted during THIS request can still be verified
// by the NEXT one. The only path that needs it is the cookie-less GET whose
// session becomes non-empty mid-response (CSRFLoad mints the CSRF token; the
// login/register page embeds a CAPTCHA): at issue time there is no identifier
// anywhere yet — neither cookie nor context — and scs generates the fresh
// token lazily inside Commit(), after the handler has already rendered. We
// pre-generate with scs' own generator (32 CSPRNG bytes, base64url) and
// commit it explicitly via Load(), which flips the session to Modified so the
// very same value lands in the Set-Cookie header.
//
// Failures here are non-fatal: losing the stash only means the next form post
// re-solves a fresh challenge, never a security downgrade.
func (a *App) recordPendingToken(ctx context.Context) {
	if pending := a.sm.GetString(ctx, pendingTokenKey); pending != "" {
		// A rotation point (logout, login, expiry reset) already committed this
		// response's identifier; challenges minted now must bind to exactly it.
		// Re-commit so the store holds the final data under that token even if
		// nothing else in the request marks the session Modified (Commit is
		// idempotent for the identifier — it never re-mints an existing one).
		if _, _, err := a.sm.Commit(ctx); err != nil {
			log.Printf("auth: pending session token recommit failed (non-fatal): %v", sanitizedErr(err))
		}
		return
	}
	if a.sm.Token(ctx) != "" {
		return // an identifier already exists (cookie present or committed)
	}
	tok, err := NewRandomToken(32)
	if err != nil {
		log.Printf("auth: pending session token generation failed (non-fatal): %v", sanitizedErr(err))
		return
	}
	if _, err := a.sm.Load(ctx, tok); err != nil {
		log.Printf("auth: pending session token commit failed (non-fatal): %v", sanitizedErr(err))
		return
	}
	a.sm.Put(ctx, pendingTokenKey, tok)
}

// newChallenge mints the CAPTCHA embedded in the next form render. When this
// request has no session identifier yet (cookie-less GET), the soon-to-be-
// committed token is stashed first so requireFriction on the following POST
// can reconstruct the exact binding anchor.
func (a *App) newChallenge(w http.ResponseWriter, r *http.Request, purpose string) *CaptchaChallenge {
	a.recordPendingToken(r.Context())
	ch, err := a.captcha.Issue(r.Context(), a.sessionTokenForRequest(r), CurrentUserID(r.Context(), a.sm), purpose)
	if err != nil {
		// Debug aid: writeGenericError swallows the cause; without this line
		// every captcha-mint 500 is indistinguishable in tests/ops.
		log.Printf("server: captcha issue failed purpose=%q: %v", purpose, err)
		writeGenericError(w)
		return nil
	}
	return ch
}

// ---- register -------------------------------------------------------------

func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if CurrentUserID(r.Context(), a.sm) != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil {
		return
	}
	a.render(w, "register", http.StatusOK, data)
}

func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm() // best-effort: missing body just fails validation below
	data := a.baseData(r)

	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil {
			return
		}
		a.render(w, "register", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) {
		// Silent drop: same shape as a failed CAPTCHA, zero signal to the bot.
		fail("challenge failed, please try again")
		return
	}
	if !a.requireFriction(w, r, "register") {
		fail("challenge failed, please try again")
		return
	}

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if err := ValidateUsername(username); err != nil {
		fail("invalid username: must be 3-32 characters (lowercase letters, digits, underscore)")
		return
	}
	if err := ValidatePasswordStrength(password); err != nil {
		fail("weak password: must be 10-128 characters")
		return
	}
	// Confirm-password equality over digests keeps the comparison bounded-time
	// even though both values came from the same client.
	if h1, h2 := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(r.FormValue("password_confirm"))); subtle.ConstantTimeCompare(h1[:], h2[:]) != 1 {
		fail("passwords do not match")
		return
	}

	// Hash BEFORE touching the store: argon2id is also the throttle that makes
	// mass-registration expensive (SECURITY_AND_OPSEC.md §1).
	hash, _, err := HashPassword(password)
	if err != nil {
		fail("challenge failed, please try again") // ErrAuthInternal — generic
		return
	}
	if _, err := a.auth.AuthCreateUser(ctx, username, hash, "active"); err != nil {
		// A unique violation reads as "taken"; anything else stays generic so
		// DB internals never reach the client.
		if isUniqueViolation(err) {
			fail("that username is unavailable")
		} else {
			fail("registration failed, please try again")
		}
		return
	}
	data.Flash = "Account created — you can log in now."
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

// isUniqueViolation detects SQLSTATE 23505/23503 family without importing the
// full pgconn surface into every error path.
func isUniqueViolation(err error) bool {
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) {
		s := pe.SQLState()
		return strings.HasPrefix(s, "23")
	}
	return false
}

// ---- login ----------------------------------------------------------------

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if CurrentUserID(ctx, a.sm) != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)

	const badCreds = "invalid username or password"
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
			return
		}
		a.render(w, "login", http.StatusUnauthorized, data)
	}

	if honeypotTriggered(r) {
		fail(badCreds)
		return
	}
	if !a.requireFriction(w, r, "login") {
		fail("challenge failed, please try again")
		return
	}

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" {
		fail(badCreds)
		return
	}

	user, err := a.auth.AuthUserByUsername(ctx, username)
	switch {
	case errors.Is(err, ErrNoSuchUser):
		// Equalize timing against the real verify path (AGENTS.md §4).
		DummyVerify()
		fail(badCreds)
		return
	case err != nil:
		fail("login failed, please try again")
		return
	}

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		fail(badCreds)
		return
	}
	if user.Status != "active" {
		fail("this account cannot sign in right now")
		return
	}

	// Transparent parameter upgrade: re-hash at current baseline once the
	// stored record lags behind (records carry their own params by design).
	if p, _, perr := decodeArgon(user.PasswordHash); perr == nil && NeedsUpgrade(p) {
		if nh, _, herr := HashPassword(password); herr == nil {
			if uerr := a.auth.AuthUpdatePasswordHash(ctx, user.ID, nh); uerr != nil {
				log.Printf("auth: password param upgrade failed (non-fatal): %v", sanitizedErr(uerr))
			}
		}
	}

	// Session rotation on authentication (AGENTS.md §4): the pre-login token
	// (and anything a fixation attacker seeded into it) dies here. Stash the
	// fresh identifier immediately: scs' lazy-commit only writes the new token
	// at response flush, so without this every later handler in this request
	// (and the redirect target's challenge binding) would anchor to a value
	// nothing can reproduce — the exact rotated-session mismatch that
	// sessionTokenForRequest documents. The stash rides along in the committed
	// session and self-heals: requireFriction Pop's it after one successful
	// verify, and recordPendingToken re-commits while it lives.
	if err := RotateOnAuth(ctx, a.sm); err != nil {
		writeGenericError(w)
		return
	}
	a.sm.Put(ctx, pendingTokenKey, a.sm.Token(ctx))
	a.sm.Put(ctx, userIDKey, uuidString(user.ID))
	a.sm.Put(ctx, usernameKey, user.Username)
	a.sm.Put(ctx, roleKey, user.Role)
	a.sm.Put(ctx, createdKey, time.Now().UTC().Unix()) // absolute clock restarts at auth

	if err := a.auth.AuthRecordLogin(ctx, user.ID, time.Now().UTC(), r.Host); err != nil {
		// Login history is defense-in-depth, not a gate: never block a good
		// login over a bookkeeping failure, but do say something in the log.
		log.Printf("auth: login history write failed (non-fatal): %v", sanitizedErr(err))
	}

	// Stamp sessions.user_id on the durable copy so admin-side session
	// invalidation (role change / suspension) can find this row later.
	// Non-fatal by design — same reasoning as login history above.
	a.linkSessionUser(ctx)

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// linkSessionUser stamps user_id on the durable session row keyed by the
// token THIS response carries (post-rotation). Nil linker = in-memory dev
// mode; nothing to stamp and no security decision depends on it elsewhere.
func (a *App) linkSessionUser(ctx context.Context) {
	if a.linker == nil {
		return
	}
	uid := CurrentUserID(ctx, a.sm)
	tok := a.sm.Token(ctx)
	if uid == "" || tok == "" {
		return
	}
	if err := a.linker.LinkUser(ctx, tok, uid); err != nil {
		log.Printf("auth: session user_id stamp failed (non-fatal): %v", sanitizedErr(err))
	}
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := a.sm.Destroy(ctx); err != nil {
		writeGenericError(w)
		return
	}
	// scs' RenewToken only flips status to Modified — the fresh identifier is
	// committed lazily at response flush, AFTER this handler has already
	// rendered. Minting the login-page challenge against that timeline leaves
	// it bound to a token the next request cannot reproduce (the POST carries
	// the new cookie, but requireFriction's ctx token is still "" until
	// LoadAndSave populates it). Commit the rotated session NOW and stash the
	// resulting identifier so issue-time and verify-time anchors agree.
	if err := a.rotateToFreshToken(ctx); err != nil {
		writeGenericError(w)
		return
	}
	data := a.baseData(r)
	data.Flash = "Signed out."
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

// ---- account / personal phrase ----------------------------------------------

// PersonalPhraseLength bounds the anti-phishing phrase (migration 0006 CHECK
// mirrors these numbers; change both together).
const (
	PersonalPhraseMin = 4
	PersonalPhraseMax = 64
)

// ValidatePersonalPhrase rejects blank/control-heavy phrases. Anything else
// printable is fine — the phrase is user-chosen display text, never a secret
// credential (it is shown pre-authentication-cost: only after a live session).
func ValidatePersonalPhrase(p string) error {
	p = strings.TrimSpace(p)
	if len(p) < PersonalPhraseMin || len(p) > PersonalPhraseMax {
		return fmt.Errorf("server: phrase must be %d-%d characters", PersonalPhraseMin, PersonalPhraseMax)
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f { // no control characters
			return errors.New("server: phrase may not contain control characters")
		}
	}
	return nil
}

func (a *App) handleAccount(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	// PGP state + the shared account-page CAPTCHA slot. Minted against the
	// same anchor requireFriction reconstructs on POST (sessionTokenForRequest
	// handles the rotated-session fallback consistently for both hops).
	a.loadPGPState(r.Context(), &data)
	if data.Captcha = a.newChallenge(w, r, accountPurpose); data.Captcha == nil {
		return
	}
	a.render(w, "account", http.StatusOK, data)
}

func (a *App) handlePhraseSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	// Every re-render carries a FRESH challenge, exactly like handlePGPUpload:
	// requireFriction burns the submitted puzzle on success (single-use per
	// AGENTS.md §3), so reusing the GET-minted state would emit dead tokens —
	// and rendering without any Captcha at all trips form_friction's nil guard
	// into broken forms. newChallenge answers the 500 itself when minting fails.
	fail := func(msg string) {
		data.Error = msg
		data.PersonalPhrase = a.sm.GetString(ctx, phraseKey)
		if data.Captcha = a.newChallenge(w, r, accountPurpose); data.Captcha == nil {
			return
		}
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}
	if honeypotTriggered(r) {
		fail("submission rejected")
		return
	}
	// The account page embeds one CAPTCHA slot shared by every POST form
	// (phrase, PGP upload, PGP clear); the challenge is minted with this
	// purpose and must be verified with it here. A wrong answer keeps the
	// challenge alive (bounded retries), a correct one burns it.
	if !a.requireFriction(w, r, accountPurpose) {
		fail("challenge failed, please try again")
		return
	}
	phrase := strings.TrimSpace(r.FormValue("personal_phrase"))
	if err := ValidatePersonalPhrase(phrase); err != nil {
		fail(err.Error())
		return
	}
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		// Session claims a login but carries no parsable UUID — treat the
		// session as broken rather than trusting partial identity state.
		writeGenericError(w)
		return
	}
	if err := a.auth.AuthSetPersonalPhrase(ctx, *uid, phrase); err != nil {
		fail("could not save the phrase, please try again")
		return
	}
	a.sm.Put(ctx, phraseKey, phrase)
	data.PersonalPhrase = phrase
	data.Flash = "Personal phrase updated."
	// Fresh challenge for the post-success render (the submitted one was just
	// burned); mirrors handlePGPUpload's success path.
	if data.Captcha = a.newChallenge(w, r, accountPurpose); data.Captcha == nil {
		return
	}
	a.render(w, "account", http.StatusOK, data)
}

// ---- session identity helpers ---------------------------------------------

// uuidString renders a pgtype.UUID as its canonical 8-4-4-4-12 text form.
// Invalid/NULL UUIDs become "" — callers treat that as "no identity" rather
// than trusting partial state (handlePhraseSubmit relies on this).
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	i := 0
	for n, v := range [16]byte(b) {
		if n == 4 || n == 6 || n == 8 || n == 10 {
			out[i] = '-'
			i++
		}
		out[i] = hex[v>>4]
		out[i+1] = hex[v&0x0f]
		i += 2
	}
	return string(out)
}

// parseUUID decodes the canonical text form back into a pgtype.UUID
// (ok=false for anything malformed — sessions claiming an unparseable id are
// treated as broken, never trusted). Hand-decoded so parseUUID stays the exact
// inverse of uuidString with no dependency on driver Scan quirks: pgtype's
// Scan historically rejected some canonical strings depending on version,
// which surfaced as silent 500s from identity-dependent POST handlers.
func parseUUID(s string) (pgtype.UUID, bool) {
	// Canonical 8-4-4-4-12 form only; anything malformed returns ok=false and
	// callers treat that as "broken session", failing closed.
	s = strings.TrimSpace(s)
	if len(s) != 36 {
		return pgtype.UUID{}, false
	}
	var b [16]byte
	pos := 0
	nibble := -1
	for i := 0; i < 36; i++ {
		c := s[i]
		if c == '-' {
			// Hyphens must appear exactly at 8-4-4-4-12 boundaries.
			if i != 8 && i != 13 && i != 18 && i != 23 {
				return pgtype.UUID{}, false
			}
			continue
		}
		v := byte(0xff)
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return pgtype.UUID{}, false
		}
		if nibble < 0 {
			nibble = int(v)
		} else {
			b[pos] = byte(nibble<<4) | v
			pos++
			nibble = -1
		}
	}
	if pos != 16 || nibble >= 0 {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: b, Valid: true}, true
}

// currentUserUUID resolves the session's user_id claim back to a database
// UUID. nil means anonymous or corrupt — handlers must fail closed.
func currentUserUUID(ctx context.Context, sm *scs.SessionManager) *pgtype.UUID {
	id := sm.GetString(ctx, userIDKey)
	if id == "" {
		return nil
	}
	u, ok := parseUUID(id)
	if !ok {
		return nil
	}
	return &u
}

// ---- template rendering -----------------------------------------------------

// templateFuncs exposes {{inertURL .Text}} to templates: URLs become escaped
// plain text wrapped in a marker span, NEVER anchor tags (ARCHITECTURE.md §8
// anti-phishing rule). html/template auto-escapes everything else.
var templateFuncs = template.FuncMap{
	"inertURL": func(s string) template.HTML {
		fields := strings.Fields(s)
		out := make([]string, len(fields))
		for i, f := range fields {
			lf := strings.ToLower(f)
			if strings.HasPrefix(lf, "http://") || strings.HasPrefix(lf, "https://") {
				out[i] = `<span class="inert-url">` + template.HTMLEscapeString(f) + `</span>`
				continue
			}
			out[i] = template.HTMLEscapeString(f)
		}
		return template.HTML(strings.Join(out, " "))
	},
}

// buildTemplates parses every template under web/templates (layout + shared
// partials + one page per set). Exposed for tests and main.go boot
// verification — a broken template must fail at startup, not on first
// request. Each parse is layout + all partials + ONE page: Go templates
// resolve {{template "x"}} by name, so parsing several *pages* in one set
// would let the last body silently shadow the others. Partials (anything
// that is neither layout nor page_*, e.g. form_friction.html defining
// "friction") MUST ride along in every set — pages reference them by name
// and an execution-time "no such template" error after headers are sent is
// unrecoverable (see render()).
func (a *App) buildTemplates(dir string) error {
	layout := filepath.Join(dir, "layout.html")
	pages, err := filepath.Glob(filepath.Join(dir, "page_*.html"))
	if err != nil {
		return fmt.Errorf("server: template glob: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Errorf("server: no page templates under %s", dir)
	}
	partials, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		return fmt.Errorf("server: template glob: %w", err)
	}
	var extras []string
	for _, p := range partials {
		base := filepath.Base(p)
		if base == "layout.html" || strings.HasPrefix(base, "page_") {
			continue
		}
		extras = append(extras, p)
	}
	tpls := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		name := strings.TrimSuffix(filepath.Base(p), ".html") // "page_login" -> "page_login"
		files := append([]string{layout}, extras...)
		files = append(files, p)
		t, err := template.New(name).Funcs(templateFuncs).ParseFiles(files...)
		if err != nil {
			return fmt.Errorf("server: parse template %s: %w", p, err)
		}
		// render() looks pages up by their short name.
		tpls[strings.TrimPrefix(name, "page_")] = t
	}
	a.templates = tpls
	return nil
}

func (a *App) baseData(r *http.Request) AuthData {
	d := AuthData{CsrfToken: CSRFToken(r.Context(), a.sm)}
	d.HoneypotHidden = true // forms always hide the decoy via CSS (AGENTS.md §3)
	return d
}

func (a *App) render(w http.ResponseWriter, name string, code int, data AuthData) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	// Buffer first: an execution error after headers are out is unrecoverable
	// and would leave the client with a truncated 200. Rendering into memory
	// lets a failure still answer the mandated generic 500 (these pages are
	// small by design — no streaming bodies in Phase 3).
	var buf bytes.Buffer
	// Each stored set was parsed as template.New("page_x").ParseFiles(layout,
	// partials..., page). Because the page file starts with
	// {{template "layout" .}} and only DEFINES "title"/"body", the page's own
	// content lives inside the "layout" template once parsed; layout invokes
	// those blocks at execution time. So execute "layout" — NOT the short name
	// ("login" is undefined inside the set) and NOT the raw page root (it is
	// an empty shell whose only statement is the layout call). The debug log
	// keeps the RAW error text: template errors never contain user secrets,
	// and sanitizedErr() here previously disguised every render failure as
	// "redis: operation failed".
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

var _ chi.Router // reserved: route mounting stays chi-typed (AGENTS.md §2)
var _ = scs.New  // reserved: session manager constructor reference
