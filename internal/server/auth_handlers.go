package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
)

const honeypotField = "sta"

type AuthData struct {
	VendorApplicationPending bool
	IsSignedIn       bool
	CsrfToken       string
	IsAuthenticated bool
	IsAdmin         bool
	IsVendor        bool
	Captcha         *CaptchaChallenge
	HoneypotHidden  bool
	Error           string
	Flash           string
	PersonalPhrase  string
	PGPPublicKey    string
	PGPFingerprint  string
        PreferredCurrency string
	Products        []ProductRecord
	Categories      []CategoryRecord
	Pending         []VendorRecord
	VendorState     *VendorRecord
	Requires2FA     bool
	Pending2FAUID   string
	TOTPEnabled     bool
	TOTPQRCode      template.URL
	TOTPSecret      string
	MessagesUnread  int64
	BackupCodes     []string
	ShipsFrom       string
	ShipsFromOptions []string
}

type UserRecord struct {
	ID           pgtype.UUID
	Username     string
	PasswordHash string
	Role         string
	Status       string
	TOTPSecret   string
	TOTPEnabled  bool
	BackupCodes  string
	CreatedAt    time.Time
}

type SessionInvalidator interface {
	InvalidateUserSessions(ctx context.Context, userID string) error
}

type AuthStore interface {
	AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error)
	AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error)
	AuthUpdatePasswordHash(ctx context.Context, id pgtype.UUID, passwordHash string) error
	AuthSetPersonalPhrase(ctx context.Context, id pgtype.UUID, phrase string) error
	AuthRecordLogin(ctx context.Context, userID pgtype.UUID, at time.Time, host string) error
	AuthGetPGPKey(ctx context.Context, id pgtype.UUID) (string, error)
	AuthSetPGPKey(ctx context.Context, id pgtype.UUID, armoredPublicKey string) error
	AuthClearPGPKey(ctx context.Context, id pgtype.UUID) error
	VendorCreateRequest(ctx context.Context, userID pgtype.UUID, shopName string) error
	VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error)
	VendorListPending(ctx context.Context) ([]VendorRecord, error)
	VendorGetByID(ctx context.Context, vendorID pgtype.UUID) (*VendorRecord, error)
	VendorByShopName(ctx context.Context, shopName string) (*VendorRecord, error)
	VendorSetApproval(ctx context.Context, vendorID pgtype.UUID, approved bool) error
	VendorSetRoleToVendor(ctx context.Context, userID pgtype.UUID) error
	VendorUsername(ctx context.Context, vendorUserID pgtype.UUID) (string, error)
	AuthUserRole(ctx context.Context, id pgtype.UUID) (string, error)
	AuthUserByID(ctx context.Context, id pgtype.UUID) (*UserRecord, error)
	AuditAppend(ctx context.Context, actorID pgtype.UUID, actorKind, action, target, details string) error
}

type SessionLinker interface {
	LinkUser(ctx context.Context, token, userID string) error
}

var ErrNoSuchUser = errors.New("store: no such user")

const (
	qAuthUserByUsername     = `SELECT id, username, password_hash, role, status, COALESCE(totp_secret, ''), COALESCE(totp_enabled, false), COALESCE(backup_codes, ''), created_at FROM public.users WHERE username = $1`
	qAuthUserByID           = `SELECT id, username, password_hash, role, status, COALESCE(totp_secret, ''), COALESCE(totp_enabled, false), COALESCE(backup_codes, ''), created_at FROM public.users WHERE id = $1`
	qAuthCreateUser         = `INSERT INTO public.users (username, password_hash, role, status) VALUES ($1, $2, 'customer', $3) RETURNING id, username, password_hash, role, status, '', false, '', created_at`
	qAuthUpdatePasswordHash = `UPDATE public.users SET password_hash = $2, updated_at = now() WHERE id = $1`
	qAuthSetPhrase          = `UPDATE public.users SET personal_phrase = $2, updated_at = now() WHERE id = $1`
	qAuthRecordLogin        = `INSERT INTO public.login_history (user_id, occurred_at, host_header) VALUES ($1, $2, $3)`
	qAuthGetPGPKey          = `SELECT COALESCE(pgp_public_key, '') FROM public.users WHERE id = $1`
	qAuthSetPGPKey          = `UPDATE public.users SET pgp_public_key = $2, updated_at = now() WHERE id = $1`
	qAuthClearPGPKey        = `UPDATE public.users SET pgp_public_key = NULL, updated_at = now() WHERE id = $1`
)

type PGAuthStore struct{ pool *pgxpool.Pool }
func NewPGAuthStore(pool *pgxpool.Pool) *PGAuthStore { return &PGAuthStore{pool: pool} }

func scanUser(row pgx.Row) (*UserRecord, error) {
	u := &UserRecord{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status, &u.TOTPSecret, &u.TOTPEnabled, &u.BackupCodes, &u.CreatedAt)
	if err != nil { return nil, err }
	return u, nil
}

func (s *PGAuthStore) AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthUserByUsername, username))
	if errors.Is(err, pgx.ErrNoRows) { return nil, ErrNoSuchUser }
	if err != nil { return nil, fmt.Errorf("store: get user: %w", err) }
	return u, nil
}

func (s *PGAuthStore) AuthUserByID(ctx context.Context, id pgtype.UUID) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthUserByID, id))
	if errors.Is(err, pgx.ErrNoRows) { return nil, ErrNoSuchUser }
	if err != nil { return nil, fmt.Errorf("store: get user by id: %w", err) }
	return u, nil
}

func (s *PGAuthStore) AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthCreateUser, username, passwordHash, status))
	if err != nil { return nil, fmt.Errorf("store: create user: %w", err) }
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
		if errors.Is(err, pgx.ErrNoRows) { return "", ErrNoSuchUser }
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

const (
	qVendorCreate        = `INSERT INTO public.vendors (user_id, shop_name, approved) VALUES ($1, $2, FALSE)`
	qVendorByUser        = `SELECT id, user_id, shop_name, approved, created_at FROM public.vendors WHERE user_id = $1`
	qVendorPending       = `SELECT v.id, v.user_id, v.shop_name, v.approved, v.created_at FROM public.vendors v JOIN public.users u ON u.id = v.user_id WHERE NOT v.approved AND u.role = 'customer' ORDER BY v.created_at ASC LIMIT 200`
	qVendorSetApproval   = `UPDATE public.vendors SET approved = $2 WHERE id = $1`
	qVendorUsername      = `SELECT username FROM public.users WHERE id = $1`
	qAuthUserRole        = `SELECT role FROM public.users WHERE id = $1`
	qVendorPromoteRole   = `UPDATE public.users SET role = 'vendor', updated_at = now() WHERE id = $1 AND role = 'customer'`
	qVendorByID          = `SELECT id, user_id, shop_name, approved, created_at FROM public.vendors WHERE id = $1`
	qVendorByShopName    = `SELECT id, user_id, lower(shop_name), approved, created_at FROM public.vendors WHERE lower(shop_name) = $1`
	qCategoryList        = `SELECT id, slug, name FROM public.categories ORDER BY name ASC LIMIT 200`
	qProductInsert       = `INSERT INTO public.products (vendor_id, category_id, title, description, price_usd_cents, stock, image_path, status) VALUES ($1, NULLIF($2, 0), $3, $4, $5, $6, NULLIF($7, ''), $8) RETURNING id`
	qProductGet          = `SELECT id, vendor_id, COALESCE(category_id, 0), title, description, price_usd_cents, stock, COALESCE(image_path, ''), status, created_at, updated_at FROM public.products WHERE id = $1`
	qProductUpdate       = `UPDATE public.products SET category_id = NULLIF($2, 0), title = $3, description = $4, price_usd_cents = $5, stock = $6, image_path = NULLIF($7, ''), status = $8, updated_at = now() WHERE id = $1`
	qProductSetStatus    = `UPDATE public.products SET status = $2, updated_at = now() WHERE id = $1`
	qProductsByVendor    = `SELECT id, vendor_id, COALESCE(category_id, 0), title, description, price_usd_cents, stock, COALESCE(image_path, ''), status, created_at, updated_at FROM public.products WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT 500`
	qProductsActive      = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description, p.price_usd_cents, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at FROM public.products p JOIN public.vendors v ON v.id = p.vendor_id WHERE p.status = 'active' AND v.approved AND (NULLIF($1, 0::bigint) IS NULL OR p.category_id = NULLIF($1, 0::bigint)) ORDER BY p.created_at DESC LIMIT $2`
	qProductsSearch      = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description, p.price_usd_cents, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at FROM public.products p JOIN public.vendors v ON v.id = p.vendor_id WHERE p.status = 'active' AND v.approved AND to_tsvector('simple', p.title || ' ' || p.description) @@ plainto_tsquery('simple', $1) ORDER BY ts_rank(to_tsvector('simple', p.title || ' ' || p.description), plainto_tsquery('simple', $1)) DESC LIMIT $2`
	qShopNameForID       = `SELECT shop_name FROM public.vendors WHERE id = $1 AND approved`
	qApprovedVendorID    = `SELECT id FROM public.vendors WHERE user_id = $1 AND approved`
	qAuditAppend         = `INSERT INTO public.audit_log (actor_id, actor_kind, action, target, details) VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, '')::jsonb, '{}'::jsonb))`
)

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
	if _, err := s.pool.Exec(ctx, qVendorCreate, userID, shopName); err != nil { return fmt.Errorf("store: create vendor request: %w", err) }
	return nil
}
func (s *PGAuthStore) VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByUser, userID))
	if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("store: get vendor: %w", err) }
	return &v, nil
}
func (s *PGAuthStore) VendorListPending(ctx context.Context) ([]VendorRecord, error) {
	rows, err := s.pool.Query(ctx, qVendorPending)
	if err != nil { return nil, fmt.Errorf("store: list pending vendors: %w", err) }
	defer rows.Close()
	out := []VendorRecord{}
	for rows.Next() {
		v, err := scanVendor(rows)
		if err != nil { return nil, fmt.Errorf("store: list pending vendors: scan: %w", err) }
		out = append(out, v)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("store: list pending vendors: %w", err) }
	return out, nil
}
func (s *PGAuthStore) VendorGetByID(ctx context.Context, vendorID pgtype.UUID) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByID, vendorID))
	if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("store: get vendor by id: %w", err) }
	return &v, nil
}
func (s *PGAuthStore) VendorByShopName(ctx context.Context, shopName string) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qVendorByShopName, strings.ToLower(shopName)))
	if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("store: get vendor by shop name: %w", err) }
	return &v, nil
}
func (s *PGAuthStore) CategoryList(ctx context.Context) ([]CategoryRecord, error) {
	rows, err := s.pool.Query(ctx, qCategoryList)
	if err != nil { return nil, fmt.Errorf("store: category list: %w", err) }
	defer rows.Close()
	out := []CategoryRecord{}
	for rows.Next() {
		var c CategoryRecord
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name); err != nil { return nil, fmt.Errorf("store: category list: scan: %w", err) }
		out = append(out, c)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("store: category list: %w", err) }
	return out, nil
}
func (s *PGAuthStore) ProductCreate(ctx context.Context, p *ProductRecord) error {
	return s.pool.QueryRow(ctx, qProductInsert, p.VendorID, p.CategoryID, p.Title, p.Description, p.PriceUSDCents, p.Stock, p.ImagePath, p.Status).Scan(&p.ID)
}
func (s *PGAuthStore) ProductGet(ctx context.Context, id pgtype.UUID) (*ProductRecord, error) {
	p, err := scanProduct(s.pool.QueryRow(ctx, qProductGet, id))
	if errors.Is(err, pgx.ErrNoRows) { return nil, ErrNoSuchUser }
	if err != nil { return nil, fmt.Errorf("store: get product: %w", err) }
	return &p, nil
}
func (s *PGAuthStore) ProductUpdate(ctx context.Context, p *ProductRecord) error {
	_, err := s.pool.Exec(ctx, qProductUpdate, p.ID, p.CategoryID, p.Title, p.Description, p.PriceUSDCents, p.Stock, p.ImagePath, p.Status)
	if err != nil { return fmt.Errorf("store: update product: %w", err) }
	return nil
}
func (s *PGAuthStore) ProductSetStatus(ctx context.Context, id pgtype.UUID, status string) error {
	if _, err := s.pool.Exec(ctx, qProductSetStatus, id, status); err != nil { return fmt.Errorf("store: set product status: %w", err) }
	return nil
}
func (s *PGAuthStore) queryProducts(ctx context.Context, sql string, args ...any) ([]ProductRecord, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil { return nil, fmt.Errorf("store: product query: %w", err) }
	defer rows.Close()
	out := []ProductRecord{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil { return nil, fmt.Errorf("store: product query: scan: %w", err) }
		out = append(out, p)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("store: product query: %w", err) }
	return out, nil
}
func (s *PGAuthStore) ProductListByVendor(ctx context.Context, vendorID pgtype.UUID) ([]ProductRecord, error) { return s.queryProducts(ctx, qProductsByVendor, vendorID) }
func (s *PGAuthStore) ProductListActive(ctx context.Context, categoryID int64, limit int) ([]ProductRecord, error) { return s.queryProducts(ctx, qProductsActive, categoryID, limit) }
func (s *PGAuthStore) ProductSearch(ctx context.Context, query string, limit int) ([]ProductRecord, error) { return s.queryProducts(ctx, qProductsSearch, query, limit) }
func (s *PGAuthStore) ShopNameForID(ctx context.Context, vendorID pgtype.UUID) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, qShopNameForID, vendorID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) { return "", nil }
	if err != nil { return "", fmt.Errorf("store: shop name: %w", err) }
	return name, nil
}
func (s *PGAuthStore) ApprovedVendorID(ctx context.Context, userID pgtype.UUID) (*pgtype.UUID, error) {
	var id pgtype.UUID
	err := s.pool.QueryRow(ctx, qApprovedVendorID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("store: approved vendor id: %w", err) }
	return &id, nil
}
func (s *PGAuthStore) VendorSetApproval(ctx context.Context, vendorID pgtype.UUID, approved bool) error {
	if _, err := s.pool.Exec(ctx, qVendorSetApproval, vendorID, approved); err != nil { return fmt.Errorf("store: set vendor approval: %w", err) }
	return nil
}
func (s *PGAuthStore) VendorSetRoleToVendor(ctx context.Context, userID pgtype.UUID) error {
	if _, err := s.pool.Exec(ctx, qVendorPromoteRole, userID); err != nil { return fmt.Errorf("store: promote role: %w", err) }
	return nil
}
func (s *PGAuthStore) AuditAppend(ctx context.Context, actorID pgtype.UUID, actorKind, action, target, details string) error {
	if actorKind == "" { actorKind = "user" }
	if _, err := s.pool.Exec(ctx, qAuditAppend, actorID, actorKind, action, target, details); err != nil { return fmt.Errorf("store: audit append: %w", err) }
	return nil
}
func (s *PGAuthStore) VendorUsername(ctx context.Context, vendorUserID pgtype.UUID) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, qVendorUsername, vendorUserID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) { return "", nil }
	if err != nil { return "", fmt.Errorf("store: vendor username: %w", err) }
	return name, nil
}
func (s *PGAuthStore) AuthUserRole(ctx context.Context, id pgtype.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, qAuthUserRole, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) { return "", ErrNoSuchUser }
	if err != nil { return "", fmt.Errorf("store: get role: %w", err) }
	return role, nil
}

func (a *App) RegisterAuthRoutes(auth AuthStore, captcha *CaptchaService, sessions SessionLinker, templateDir string) error {
	return a.RegisterAuthRoutesFull(auth, captcha, sessions, nil, nil, templateDir)
}

func (a *App) RegisterAuthRoutesFull(auth AuthStore, captcha *CaptchaService, sessions SessionLinker, invalid SessionInvalidator, catalog VendorStore, templateDir string) error {
	if err := a.buildTemplates(templateDir); err != nil { return err }
	installGenericErrorRenderer(func(w http.ResponseWriter) { a.renderStaticError(w, http.StatusInternalServerError, "Something went wrong on our side. Please try again.") })
	genericErrorRenderer = func(w http.ResponseWriter) { a.renderStaticError(w, http.StatusInternalServerError, "Something went wrong on our side. Please try again.") }
	if invalid == nil && sessions != nil {
		if si, ok := sessions.(SessionInvalidator); ok { invalid = si }
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
	r.Post("/currency/set", http.HandlerFunc(a.handleSetCurrency))
	r.Get("/register", a.handleRegisterForm)
	r.Post("/register", a.handleRegisterSubmit)
	r.Get("/login", a.handleLoginForm)
	r.Post("/login", a.handleLoginSubmit)
	r.Get("/login/2fa", a.handle2FALoginForm)
	r.Post("/login/2fa", a.handle2FALoginSubmit)
	r.Post("/logout", a.handleLogout)
	
	accountMW := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/account", accountMW(http.HandlerFunc(a.handleAccount)))
	r.Method(http.MethodPost, "/account/phrase", accountMW(http.HandlerFunc(a.handlePhraseSubmit)))
	r.Method(http.MethodPost, "/account/pgp", accountMW(http.HandlerFunc(a.handlePGPUpload)))
	r.Method(http.MethodPost, "/account/pgp/clear", accountMW(http.HandlerFunc(a.handlePGPClear)))
	r.Method(http.MethodGet, "/account/2fa/setup", accountMW(http.HandlerFunc(a.handle2FASetup)))
	r.Method(http.MethodPost, "/account/2fa/enable", accountMW(http.HandlerFunc(a.handle2FAEnable)))
	r.Method(http.MethodPost, "/account/2fa/disable", accountMW(http.HandlerFunc(a.handle2FADisable)))
	
	r.Get("/pgp", a.handlePGPToolPage)
	a.registerVendorRoutes(r)
	a.registerCatalogRoutes(r)
	return nil
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.Flash = ""
	a.render(w, "home", http.StatusOK, data)
}
func (a *App) handleCaptchaImage(w http.ResponseWriter, r *http.Request) {
	if a.captcha == nil { writeGenericError(w); return }
	id := chi.URLParam(r, "id")
	ch, ok := a.captcha.Challenge(r.Context(), id)
	if !ok || len(ch.Image) == 0 { http.NotFound(w, r); return }
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ch.Image)
}
func honeypotTriggered(r *http.Request) bool { return strings.TrimSpace(r.FormValue(honeypotField)) != "" }

func (a *App) requireFriction(w http.ResponseWriter, r *http.Request, purpose string) (ok bool) {
	ctx := r.Context()
	token := a.sessionTokenForRequest(r)
	userID := CurrentUserID(ctx, a.sm)
	id := r.FormValue("captcha_id")
	ans := r.FormValue("captcha_answer")
	if err := a.captcha.Verify(ctx, r.FormValue("captcha_token"), id, ans, token, userID); err != nil { return false }
	_ = purpose
	a.captcha.Burn(ctx, id)
	a.sm.PopString(ctx, pendingTokenKey)
	return true
}

const pendingTokenKey = "pending_token"
func (a *App) sessionTokenForRequest(r *http.Request) string {
	ctx := r.Context()
	pending := a.sm.GetString(ctx, pendingTokenKey)
	if ck, err := r.Cookie(SessionCookieName); err == nil && ck.Value != "" {
		if pending != "" { return pending }
		return ck.Value
	}
	if tok := a.sm.Token(ctx); tok != "" { return tok }
	return pending
}
func (a *App) rotateToFreshToken(ctx context.Context) error {
	token, _, err := a.sm.Commit(ctx)
	if err != nil { return err }
	a.sm.Put(ctx, pendingTokenKey, token)
	return nil
}
func (a *App) recordPendingToken(ctx context.Context) {
	if pending := a.sm.GetString(ctx, pendingTokenKey); pending != "" {
		if _, _, err := a.sm.Commit(ctx); err != nil { log.Printf("auth: pending session token recommit failed (non-fatal): %v", sanitizedErr(err)) }
		return
	}
	if a.sm.Token(ctx) != "" { return }
	tok, err := NewRandomToken(32)
	if err != nil { log.Printf("auth: pending session token generation failed (non-fatal): %v", sanitizedErr(err)); return }
	if _, err := a.sm.Load(ctx, tok); err != nil { log.Printf("auth: pending session token commit failed (non-fatal): %v", sanitizedErr(err)); return }
	a.sm.Put(ctx, pendingTokenKey, tok)
}
func (a *App) newChallenge(w http.ResponseWriter, r *http.Request, purpose string) *CaptchaChallenge {
	a.recordPendingToken(r.Context())
	ch, err := a.captcha.Issue(r.Context(), a.sessionTokenForRequest(r), CurrentUserID(r.Context(), a.sm), purpose)
	if err != nil {
		log.Printf("server: captcha issue failed purpose=%q: %v", purpose, err)
		writeGenericError(w)
		return nil
	}
	return ch
}

func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if CurrentUserID(r.Context(), a.sm) != "" { http.Redirect(w, r, "/account", http.StatusSeeOther); return }
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil { return }
	a.render(w, "register", http.StatusOK, data)
}
func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil { return }
		a.render(w, "register", http.StatusUnprocessableEntity, data)
	}
	if honeypotTriggered(r) { fail("challenge failed, please try again"); return }
	if !a.requireFriction(w, r, "register") { fail("challenge failed, please try again"); return }
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if err := ValidateUsername(username); err != nil { fail("invalid username: must be 3-32 characters (lowercase letters, digits, underscore)"); return }
	if err := ValidatePasswordStrength(password); err != nil { fail("weak password: must be 10-128 characters"); return }
	if h1, h2 := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(r.FormValue("password_confirm"))); subtle.ConstantTimeCompare(h1[:], h2[:]) != 1 { fail("passwords do not match"); return }
	hash, _, err := HashPassword(password)
	if err != nil { fail("challenge failed, please try again"); return }
	if _, err := a.auth.AuthCreateUser(ctx, username, hash, "active"); err != nil {
		if isUniqueViolation(err) { fail("that username is unavailable") } else { fail("registration failed, please try again") }
		return
	}
	data.Flash = "Account created — you can log in now."
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil { return }
	a.render(w, "login", http.StatusOK, data)
}
func isUniqueViolation(err error) bool {
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) { return strings.HasPrefix(pe.SQLState(), "23") }
	return false
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if CurrentUserID(ctx, a.sm) != "" { http.Redirect(w, r, "/account", http.StatusSeeOther); return }
	a.sm.Remove(ctx, "pending_2fa_uid")
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil { return }
	a.render(w, "login", http.StatusOK, data)
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	const badCreds = "invalid username or password"
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil { return }
		a.render(w, "login", http.StatusUnauthorized, data)
	}
	if honeypotTriggered(r) { fail(badCreds); return }
	if !a.requireFriction(w, r, "login") { fail("challenge failed, please try again"); return }
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" { fail(badCreds); return }
	
	user, err := a.auth.AuthUserByUsername(ctx, username)
	switch {
	case errors.Is(err, ErrNoSuchUser): DummyVerify(); fail(badCreds); return
	case err != nil: fail("login failed, please try again"); return
	}
	
	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok { fail(badCreds); return }
	if user.Status != "active" { fail("this account cannot sign in right now"); return }

	if p, _, perr := decodeArgon(user.PasswordHash); perr == nil && NeedsUpgrade(p) {
		if nh, _, herr := HashPassword(password); herr == nil {
			if uerr := a.auth.AuthUpdatePasswordHash(ctx, user.ID, nh); uerr != nil { log.Printf("auth: password param upgrade failed (non-fatal): %v", sanitizedErr(uerr)) }
		}
	}

	// 2FA Check
	if user.TOTPEnabled {
		if err := RotateOnAuth(ctx, a.sm); err != nil { writeGenericError(w); return }
		a.sm.Put(ctx, "pending_2fa_uid", uuidString(user.ID))
		http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		return
	}

	if err := RotateOnAuth(ctx, a.sm); err != nil { writeGenericError(w); return }
	a.sm.Put(ctx, pendingTokenKey, a.sm.Token(ctx))
	a.sm.Put(ctx, userIDKey, uuidString(user.ID))
	a.sm.Put(ctx, usernameKey, user.Username)
	a.sm.Put(ctx, roleKey, user.Role)
	a.sm.Put(ctx, createdKey, time.Now().UTC().Unix())
	
	if err := a.auth.AuthRecordLogin(ctx, user.ID, time.Now().UTC(), r.Host); err != nil {
		log.Printf("auth: login history write failed (non-fatal): %v", sanitizedErr(err))
	}
	a.linkSessionUser(ctx)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (a *App) linkSessionUser(ctx context.Context) {
	if a.linker == nil { return }
	uid := CurrentUserID(ctx, a.sm)
	tok := a.sm.Token(ctx)
	if uid == "" || tok == "" { return }
	if err := a.linker.LinkUser(ctx, tok, uid); err != nil { log.Printf("auth: session user_id stamp failed (non-fatal): %v", sanitizedErr(err)) }
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := a.sm.Destroy(ctx); err != nil { writeGenericError(w); return }
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

const ( PersonalPhraseMin = 4; PersonalPhraseMax = 64 )
func ValidatePersonalPhrase(p string) error {
	p = strings.TrimSpace(p)
	if len(p) < PersonalPhraseMin || len(p) > PersonalPhraseMax { return fmt.Errorf("server: phrase must be %d-%d characters", PersonalPhraseMin, PersonalPhraseMax) }
	for i := 0; i < len(p); i++ { if p[i] < 0x20 || p[i] == 0x7f { return errors.New("server: phrase may not contain control characters") } }
	return nil
}

func (a *App) handleAccount(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	if pv := a.sm.GetString(r.Context(), phraseKey); pv != "" {
		data.PersonalPhrase = pv
	}
	a.loadPGPState(r.Context(), &data)
	if uid := currentUserUUID(r.Context(), a.sm); uid != nil {
		if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
			_ = pool.QueryRow(r.Context(), `SELECT ships_from FROM vendors WHERE user_id=$1`, *uid).Scan(&data.ShipsFrom)
		}
	}
	if uid := currentUserUUID(r.Context(), a.sm); uid != nil {
		if u, uerr := a.auth.AuthUserByID(r.Context(), *uid); uerr == nil && u != nil {
			data.TOTPEnabled = u.TOTPEnabled
			if u.BackupCodes != "" {
				data.BackupCodes = strings.Split(u.BackupCodes, ",")
			}
		}
	}
	if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
	a.render(w, "account", http.StatusOK, data)
}

func (a *App) handlePhraseSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		if pv := a.sm.GetString(ctx, phraseKey); pv != "" {
			data.PersonalPhrase = pv
		}
		if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}
	if honeypotTriggered(r) { fail("submission rejected"); return }
	if !a.requireFriction(w, r, "account") { fail("challenge failed, please try again"); return }
	phrase := strings.TrimSpace(r.FormValue("personal_phrase"))
	if err := ValidatePersonalPhrase(phrase); err != nil { fail(err.Error()); return }
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil { writeGenericError(w); return }
	if err := a.auth.AuthSetPersonalPhrase(ctx, *uid, phrase); err != nil { fail("could not save the phrase, please try again"); return }
	a.sm.Put(ctx, phraseKey, phrase)
	data.PersonalPhrase = phrase
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		if uid := CurrentUserID(r.Context(), a.sm); uid != "" {
			if u, ok := parseUUID(uid); ok {
				_, _ = pool.Exec(r.Context(), `UPDATE users SET anti_phishing_phrase=$1 WHERE id=$2`, phrase, u)
			}
		}
	}
	data.Flash = "Personal phrase updated."
	if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
	a.render(w, "account", http.StatusOK, data)
}

func (a *App) handle2FALoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pendingUID := a.sm.GetString(ctx, "pending_2fa_uid")
	if pendingUID == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data := a.baseData(r)
	data.Requires2FA = true
	data.Pending2FAUID = pendingUID
	if data.Captcha = a.newChallenge(w, r, "login2fa"); data.Captcha == nil { return }
	a.render(w, "login_2fa", http.StatusOK, data)
}

func (a *App) handle2FALoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	pendingUIDStr := a.sm.GetString(ctx, "pending_2fa_uid")
	
	fail := func(msg string) {
		data.Error = msg
		data.Requires2FA = true
		data.Pending2FAUID = pendingUIDStr
		if data.Captcha = a.newChallenge(w, r, "login2fa"); data.Captcha == nil { return }
		a.render(w, "login_2fa", http.StatusUnauthorized, data)
	}

	if pendingUIDStr == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "login2fa") {
		fail("challenge failed, please try again")
		return
	}

	uid := parseUUIDText(pendingUIDStr)
	if !uid.Valid {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	user, err := a.auth.AuthUserByID(ctx, uid)
	if err != nil || user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	totpCode := strings.TrimSpace(r.FormValue("totp_code"))
	if !totp.Validate(totpCode, user.TOTPSecret) {
		fail("invalid authentication code")
		return
	}

	a.sm.Remove(ctx, "pending_2fa_uid")
	if err := RotateOnAuth(ctx, a.sm); err != nil { writeGenericError(w); return }
	a.sm.Put(ctx, pendingTokenKey, a.sm.Token(ctx))
	a.sm.Put(ctx, userIDKey, uuidString(user.ID))
	a.sm.Put(ctx, usernameKey, user.Username)
	a.sm.Put(ctx, roleKey, user.Role)
	a.sm.Put(ctx, createdKey, time.Now().UTC().Unix())
	
	if err := a.auth.AuthRecordLogin(ctx, user.ID, time.Now().UTC(), r.Host); err != nil {
		log.Printf("auth: login history write failed (non-fatal): %v", sanitizedErr(err))
	}
	a.linkSessionUser(ctx)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (a *App) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := a.baseData(r)
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil { writeGenericError(w); return }

	user, err := a.auth.AuthUserByID(ctx, *uid)
	if err != nil { writeGenericError(w); return }

	data.TOTPEnabled = user.TOTPEnabled
	if user.TOTPEnabled {
		data.BackupCodes = strings.Split(user.BackupCodes, ",")
		if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
		a.render(w, "account", http.StatusOK, data)
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "OnionShop", AccountName: user.Username})
	if err != nil { writeGenericError(w); return }

	img, err := key.Image(200, 200)
	if err != nil { writeGenericError(w); return }
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil { writeGenericError(w); return }
	data.TOTPQRCode = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()))
	
	a.sm.Put(ctx, "pending_totp_secret", key.Secret())

	var codes []string
	for i := 0; i < 8; i++ {
		b := make([]byte, 4)
		rand.Read(b)
		codes = append(codes, strings.ToUpper(hex.EncodeToString(b)))
	}
	data.BackupCodes = codes
	data.TOTPSecret = key.Secret()
	a.sm.Put(ctx, "pending_totp_backup", strings.Join(codes, ","))

	if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
	a.render(w, "2fa_setup", http.StatusOK, data)
}

func (a *App) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil { writeGenericError(w); return }

	fail := func(msg string) {
		data.Error = msg
		data.TOTPSecret = a.sm.GetString(ctx, "pending_totp_secret")
		if s := a.sm.GetString(ctx, "pending_totp_backup"); s != "" {
			data.BackupCodes = strings.Split(s, ",")
		}
		if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
		a.render(w, "2fa_setup", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) || !a.requireFriction(w, r, "account") {
		fail("challenge failed, please try again")
		return
	}

	totpCode := strings.TrimSpace(r.FormValue("totp_code"))
	password := r.FormValue("password")
	user, err := a.auth.AuthUserByID(ctx, *uid)
	if err != nil { fail("account error"); return }

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok { fail("invalid current password"); return }

	pendingSecret := a.sm.GetString(ctx, "pending_totp_secret")
	if pendingSecret == "" { fail("setup session expired, please restart"); return }

	if !totp.Validate(totpCode, pendingSecret) {
		fail("invalid authentication code")
		return
	}

	backupCodesStr := a.sm.GetString(ctx, "pending_totp_backup")
	if backupCodesStr == "" { fail("setup session expired, please restart"); return }
	if pgStore, ok := a.auth.(*PGAuthStore); ok {
		if _, err := pgStore.pool.Exec(ctx, `UPDATE public.users SET totp_secret = $2, totp_enabled = $3, backup_codes = $4, updated_at = now() WHERE id = $1`, *uid, pendingSecret, true, backupCodesStr); err != nil {
			fail("could not enable 2FA")
			return
		}
	} else {
		fail("store error")
		return
	}

	a.sm.Remove(ctx, "pending_totp_secret")
	a.sm.Remove(ctx, "pending_totp_backup")
	data.Flash = "Two-Factor Authentication enabled. Save your backup codes!"
	data.TOTPEnabled = true
	data.BackupCodes = strings.Split(backupCodesStr, ",")
	if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
	a.render(w, "account", http.StatusOK, data)
}

func (a *App) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil { writeGenericError(w); return }

	fail := func(msg string) {
		data.Error = msg
		user, _ := a.auth.AuthUserByID(ctx, *uid)
		data.TOTPEnabled = user.TOTPEnabled
		if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) || !a.requireFriction(w, r, "account") {
		fail("challenge failed, please try again")
		return
	}

	password := r.FormValue("password")
	user, err := a.auth.AuthUserByID(ctx, *uid)
	if err != nil { fail("account error"); return }

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok { fail("invalid current password"); return }

	if pgStore, ok := a.auth.(*PGAuthStore); ok {
		if _, err := pgStore.pool.Exec(ctx, `UPDATE public.users SET totp_secret = '', totp_enabled = false, backup_codes = '', updated_at = now() WHERE id = $1`, *uid); err != nil {
			fail("could not disable 2FA")
			return
		}
	} else {
		fail("store error")
		return
	}

	data.Flash = "Two-Factor Authentication disabled."
	data.TOTPEnabled = false
	if data.Captcha = a.newChallenge(w, r, "account"); data.Captcha == nil { return }
	a.render(w, "account", http.StatusOK, data)
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid { return "" }
	b := u.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	i := 0
	for n, v := range [16]byte(b) {
		if n == 4 || n == 6 || n == 8 || n == 10 { out[i] = '-'; i++ }
		out[i] = hex[v>>4]
		out[i+1] = hex[v&0x0f]
		i += 2
	}
	return string(out)
}

func parseUUID(s string) (pgtype.UUID, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 36 { return pgtype.UUID{}, false }
	var b [16]byte
	pos := 0
	nibble := -1
	for i := 0; i < 36; i++ {
		c := s[i]
		if c == '-' { if i != 8 && i != 13 && i != 18 && i != 23 { return pgtype.UUID{}, false }; continue }
		var v byte
		switch {
		case c >= '0' && c <= '9': v = c - '0'
		case c >= 'a' && c <= 'f': v = c - 'a' + 10
		case c >= 'A' && c <= 'F': v = c - 'A' + 10
		default: return pgtype.UUID{}, false
		}
		if nibble < 0 { nibble = int(v) } else { b[pos] = byte(nibble<<4) | v; pos++; nibble = -1 }
	}
	if pos != 16 || nibble >= 0 { return pgtype.UUID{}, false }
	return pgtype.UUID{Bytes: b, Valid: true}, true
}

func currentUserUUID(ctx context.Context, sm *scs.SessionManager) *pgtype.UUID {
	id := sm.GetString(ctx, userIDKey)
	if id == "" { return nil }
	u, ok := parseUUID(id)
	if !ok { return nil }
	return &u
}

var templateFuncs = template.FuncMap{
		"fmtXMR": func(piconero int64) string {
		neg := ""
		if piconero < 0 {
			neg, piconero = "-", -piconero
		}
		return fmt.Sprintf("%s%d.%012d", neg, piconero/1000000000000, piconero%1000000000000)
	},
	"fmtFiat": func(cents int64, currency string) string {
		if ratesOracle == nil {
			return fmtUSDCents(cents)
		}
		return ratesOracle.Format(cents, currency)
	},
	"fmtUSD": func(cents int64) string {
		sign := ""
		if cents < 0 {
			sign, cents = "-", -cents
		}
		return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
	},
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
	"split": strings.Split,
	"mul": func(a, b int64) int64 { return a * b },
}

func (a *App) buildTemplates(dir string) error {
	layout := filepath.Join(dir, "layout.html")
	pages, err := filepath.Glob(filepath.Join(dir, "page_*.html"))
	if err != nil { return fmt.Errorf("server: template glob: %w", err) }
	if len(pages) == 0 { return fmt.Errorf("server: no page templates under %s", dir) }
	partials, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil { return fmt.Errorf("server: template glob: %w", err) }
	var extras []string
	for _, p := range partials {
		base := filepath.Base(p)
		if base == "layout.html" || strings.HasPrefix(base, "page_") { continue }
		extras = append(extras, p)
	}
	tpls := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		name := strings.TrimSuffix(filepath.Base(p), ".html")
		short := strings.TrimPrefix(name, "page_")
		files := append([]string{layout}, extras...)
		if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), `{{template "layout" .}}`) {
			return fmt.Errorf("server: template %s must start with {{template \"layout\" .}}", p)
		}
		files = append(files, p)
		t, err := template.New(name).Funcs(templateFuncs).ParseFiles(files...)
		if err != nil { return fmt.Errorf("server: parse template %s: %w", p, err) }
		tpls[short] = t
	}
	a.templates = tpls
	return nil
}

func (a *App) baseData(r *http.Request) AuthData {
	d := AuthData{
		CsrfToken:       CSRFToken(r.Context(), a.sm),
		IsAuthenticated: CurrentUserID(r.Context(), a.sm) != "",
		IsAdmin:         CurrentRole(r.Context(), a.sm) == "admin",
		IsVendor:        CurrentRole(r.Context(), a.sm) == "vendor",
	}
	d.HoneypotHidden = true
	curr := a.sm.GetString(r.Context(), "sess_currency")
	if curr == "" {
		curr = "USD"
	}
	d.PreferredCurrency = curr
	d.ShipsFromOptions = CountryOptions()
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		if uid := CurrentUserID(r.Context(), a.sm); uid != "" {
			if u, ok := parseUUID(uid); ok {
				d.MessagesUnread = CountUnreadForUser(r.Context(), pool, u)
			}
		}
	}
	if d.PersonalPhrase == "" {
		if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
			if uid := CurrentUserID(r.Context(), a.sm); uid != "" {
				if u, ok := parseUUID(uid); ok {
					var ph string
					if err := pool.QueryRow(r.Context(), `SELECT CASE WHEN personal_phrase <> '' THEN personal_phrase ELSE anti_phishing_phrase END FROM users WHERE id=$1`, u).Scan(&ph); err == nil {
						d.PersonalPhrase = ph
					}
				}
			}
		}
	}
	d.IsSignedIn = CurrentUserID(r.Context(), a.sm) != ""
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		if uid := CurrentUserID(r.Context(), a.sm); uid != "" {
			if u, ok := parseUUID(uid); ok {
				var approved bool
				if err := pool.QueryRow(r.Context(), `SELECT approved FROM vendors WHERE user_id=$1`, u).Scan(&approved); err == nil {
					d.VendorApplicationPending = !approved
				}
			}
		}
	}

	return d
}

func (a *App) render(w http.ResponseWriter, name string, code int, data AuthData) {
	tpl := a.templates[name]
	if tpl == nil { writeGenericError(w); return }
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

var _ chi.Router
var _ = scs.New

func (a *App) handleSetCurrency(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    _ = r.ParseForm()
    curr := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
    if curr != "USD" && curr != "EUR" && curr != "AUD" {
        curr = "USD"
    }
    a.sm.Put(ctx, "sess_currency", curr)
    
    // Redirect back to referer or home
    ref := r.Referer()
    if ref == "" {
        ref = "/"
    }
    http.Redirect(w, r, ref, http.StatusSeeOther)
}

