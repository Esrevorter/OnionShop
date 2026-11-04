package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Phase 4 catalog data layer (TASKS.md "Catalog & Vendors"). The statement
// constants below mirror db/query/catalog.sql verbatim — keep both in sync
// when columns move, exactly like the qAuth*/qVendor* constants mirror
// users.sql (see auth_handlers.go).

// ErrNotFound is the sentinel for missing catalog rows (unknown product id,
// unapproved vendor, non-owned listing). Handlers map it to 404/403 without
// echoing anything about why the row is absent.
var ErrNotFound = errors.New("store: not found")

// DefaultLeaderboardSize is the number of top vendors the cron snapshot keeps
// and the homepage widget shows when no explicit limit is requested
// (CatalogRefreshTopVendors / CatalogListTopVendors treat limit <= 0 as "use
// this default"). Ten ranks mirror TASKS.md Phase 4 "top vendors" widget.
const DefaultLeaderboardSize = 10

// ProductRecord is one row from public.products. Prices are integer satoshis
// end-to-end (never float, ARCHITECTURE.md §5); ImagePath stays nil-safe —
// only locally-served static paths ever land there (AGENTS.md §1: no CDNs).
type ProductRecord struct {
	ID          pgtype.UUID
	VendorID    pgtype.UUID
	CategoryID  int64 // 0 = uncategorized (nullable column)
	Title       string
	Description string
	PriceSat    int64
	Stock       int64
	ImagePath   string // "" when NULL
	Status      string // draft|active|removed
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// StorefrontProduct extends a product row with the vendor display fields the
// storefront needs in one pass (shop name, ETA badge, vendor username).
type StorefrontProduct struct {
	ProductRecord
	ShopName       string
	Eta            string
	VendorUsername string
}

// CategoryRecord is one row from public.categories.
type CategoryRecord struct {
	ID   int64
	Slug string
	Name string
}

// ReviewRecord is one reviewable order line as surfaced on the account page
// (mirrors ReviewableLinesForCustomer in db/query/catalog.sql). HasReview +
// Rating/Body carry the current state so the form can pre-fill inside the
// edit window; Editable applies the server-side window (ReviewEditWindow vs
// Postgres' own order_updated_at clock).
type ReviewRecord struct {
	ItemID          int64
	ProductID       pgtype.UUID
	ProductTitle    string
	Qty             int64
	OrderStatus     string
	OrderUpdatedAt  time.Time
	HasReview       bool
	Rating          int64 // 0 when no review yet
	Body            string
	ReviewCreatedAt time.Time
	Editable        bool
}

// TopVendorRow is one leaderboard entry read from top_vendors_snapshot.
type TopVendorRow struct {
	Rank           int64
	SalesCount     int64
	AvgRating      float64
	ShopName       string
	Eta            string
	VendorUsername string
}

// VendorMessage is one outbound "message vendor" note (Phase 7 replaces the
// inline INSERT with proper threads/messages rows; until then this keeps the
// button honest instead of dead).
type VendorMessage struct {
	ID         pgtype.UUID
	SenderID   pgtype.UUID
	Recipient  pgtype.UUID
	Body       string
	CreatedAt  time.Time
	ReadByRecv bool
}

// parseUUIDText converts canonical 8-4-4-4-12 text back into a pgtype.UUID.
// Invalid input yields the zero UUID (Valid=false), which every store method
// treats as "no identity" exactly like uuidString's inverse contract.
func parseUUIDText(s string) pgtype.UUID {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}
	}
	return u
}

// VendorStore is the Phase 4 catalog surface: products CRUD for vendors,
// public storefront reads, vendor profiles, reviews, and the leaderboard
// snapshot the cron maintains. *pgxpool.Pool satisfies it via PGCatalogStore;
// tests substitute an in-memory fake (no Postgres in CI, AGENTS.md).
type VendorStore interface {
	// ---- vendor profile / storefront reads ----
	CatalogGetVendorByUsername(ctx context.Context, username string) (*VendorRecord, error)
	CatalogListCategories(ctx context.Context) ([]CategoryRecord, error)
	// categoryID: -1 = all categories, >= 0 = filter. limit/offset paginate.
	CatalogListProducts(ctx context.Context, categoryID int64, query string, limit, offset int) ([]StorefrontProduct, error)
	CatalogCountProducts(ctx context.Context, categoryID int64, query string) (int64, error)
	CatalogGetProduct(ctx context.Context, id pgtype.UUID) (*StorefrontProduct, error)

	// ---- vendor product CRUD (ownership enforced by vendor_id predicates) ----
	CatalogListVendorProducts(ctx context.Context, vendorUserID pgtype.UUID) ([]ProductRecord, error)
	CatalogCreateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error)
	CatalogUpdateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error)
	CatalogRemoveProduct(ctx context.Context, productID, vendorUserID pgtype.UUID) error

	// ---- reviews (one per order line, bounded edit window) ----
	CatalogReviewableLines(ctx context.Context, customerID, vendorID pgtype.UUID) ([]ReviewRecord, error)
	CatalogSubmitReview(ctx context.Context, reviewerID pgtype.UUID, itemID int64, rating int, body string) error

	// ---- leaderboard snapshot (cron writes, homepage reads) ----
	CatalogRefreshTopVendors(ctx context.Context, limit int) (int, error)
	CatalogListTopVendors(ctx context.Context, limit int) ([]TopVendorRow, error)

	// ---- "message vendor" (Phase 7 will re-home these rows into threads) ----
	CatalogSendVendorMessage(ctx context.Context, senderID, recipientID pgtype.UUID, body string) error
}

// ---- pgx-backed implementation ------------------------------------------------

const (
	qCatalogVendorByUsername = `SELECT v.id, v.user_id, v.shop_name, v.approved, v.created_at
FROM public.vendors v JOIN public.users u ON u.id = v.user_id
WHERE u.username = $1 AND v.approved`

	qCatalogListCategories = `SELECT id, slug, name FROM public.categories ORDER BY slug`

	qCatalogListProducts = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_sat, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at,
       v.shop_name, v.eta, u.username
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
JOIN public.users u   ON u.id = v.user_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
  AND ($3::text = '' OR to_tsvector('simple', p.title || ' ' || p.description)
        @@ plainto_tsquery('simple', $3))
ORDER BY CASE WHEN $3::text = '' THEN 0 ELSE ts_rank(to_tsvector('simple', p.title || ' ' || p.description),
                 plainto_tsquery('simple', $3)) END DESC,
         p.created_at DESC
LIMIT $4 OFFSET $5`

	qCatalogCountProducts = `SELECT count(*)
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
  AND ($2::text = '' OR to_tsvector('simple', p.title || ' ' || p.description)
        @@ plainto_tsquery('simple', $2))`

	qCatalogGetProduct = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_sat, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at,
       v.shop_name, v.eta, u.username
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
JOIN public.users u   ON u.id = v.user_id
WHERE p.id = $1 AND p.status = 'active' AND v.approved`

	// Vendor-scoped listing joins through users so handlers only ever need to
	// pass the session user id — the ownership predicate lives in SQL.
	qCatalogVendorProducts = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_sat, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
WHERE v.user_id = $1
ORDER BY p.created_at DESC`

	qCatalogCreateProduct = `INSERT INTO public.products (vendor_id, category_id, title, description, price_sat, stock, image_path, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, vendor_id, COALESCE(category_id, 0), title, description, price_sat, stock,
          COALESCE(image_path, ''), status, created_at, updated_at`

	qCatalogUpdateProduct = `UPDATE public.products SET
    category_id = $3,
    title       = $4,
    description = $5,
    price_sat   = $6,
    stock       = $7,
    status      = $8,
    updated_at  = now()
WHERE id = $1 AND vendor_id = $2
RETURNING id, vendor_id, COALESCE(category_id, 0), title, description, price_sat, stock,
          COALESCE(image_path, ''), status, created_at, updated_at`

	// Soft delete: status='removed' hides the listing everywhere while
	// order_items/review FKs stay intact (hard deletes would break history).
	qCatalogRemoveProduct = `UPDATE public.products SET status = 'removed', updated_at = now()
WHERE id = $1 AND vendor_id = $2 AND status <> 'removed'
RETURNING id`

	qCatalogReviewableLines = `SELECT oi.id, oi.product_id, oi.product_title, oi.qty,
       o.status, o.updated_at,
       r.id IS NOT NULL,
       COALESCE(r.rating, 0),
       COALESCE(r.body, ''),
       r.created_at
FROM public.orders o
JOIN public.order_items oi ON oi.order_id = o.id
JOIN public.products p     ON p.id = oi.product_id
JOIN public.vendors v      ON v.id = o.vendor_id
LEFT JOIN public.reviews r ON r.order_item_id = oi.id AND r.reviewer_id = $1
WHERE o.customer_id = $1 AND v.id = $2
  AND o.status IN ('shipped', 'released')
ORDER BY o.created_at DESC`

	// Submit-time authorization in one read: line's order status/age, the
	// product's owning vendor+user, and the reviewer identity. The status gate
	// lives here so a stale form POST can never rate a cancelled/refunded
	// order, and the window check uses Postgres' own clock (UTC discipline,
	// same rule as login history).
	qCatalogReviewLineContext = `SELECT o.customer_id, o.status,
       (now() - o.updated_at) <= ($2::bigint * interval '1 hour')
FROM public.order_items oi
JOIN public.orders o   ON o.id = oi.order_id
JOIN public.products p ON p.id = oi.product_id
WHERE oi.id = $1`

	qCatalogUpsertReview = `INSERT INTO public.reviews (order_item_id, product_id, reviewer_id, rating, body)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (order_item_id) DO UPDATE
SET rating = EXCLUDED.rating, body = EXCLUDED.body, updated_at = now()`

	qCatalogTopSelect = `SELECT v.id,
       count(DISTINCT o.id)::int,
       COALESCE(round(AVG(r.rating), 2), 0)::float8
FROM public.vendors v
JOIN public.orders o ON o.vendor_id = v.id AND o.status = 'released'
LEFT JOIN public.order_items oi ON oi.order_id = o.id
LEFT JOIN public.reviews r ON r.order_item_id = oi.id
WHERE v.approved
GROUP BY v.id
HAVING count(DISTINCT o.id) > 0
ORDER BY count(DISTINCT o.id) DESC,
         COALESCE(round(AVG(r.rating), 2), 0) DESC,
         v.id
LIMIT $1`

	qCatalogDeleteSnapshot = `DELETE FROM public.top_vendors_snapshot`

	qCatalogInsertSnapshotRow = `INSERT INTO public.top_vendors_snapshot (rank, vendor_id, sales_count, avg_rating, refreshed_at)
VALUES ($1, $2, $3, $4, $5)`

	qCatalogListSnapshot = `SELECT t.rank, t.sales_count, t.avg_rating, v.shop_name, v.eta, u.username
FROM public.top_vendors_snapshot t
JOIN public.vendors v ON v.id = t.vendor_id
JOIN public.users u   ON u.id = v.user_id
ORDER BY t.rank ASC LIMIT $1`

	// Inline vendor messaging until Phase 7 owns this surface: find-or-create
	// the unordered participant pair (threads CHECK a<b enforces one row per
	// pair), then append the note. The body renders as escaped plain text
	// wherever it is displayed — never auto-linked (AGENTS.md §3).
	qCatalogThreadFindOrCreate = `SELECT id FROM public.threads
WHERE (participant_a, participant_b) = (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid))`

	qCatalogThreadCreate = `INSERT INTO public.threads (participant_a, participant_b)
VALUES (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid))
ON CONFLICT DO NOTHING
RETURNING id`

	qCatalogInsertMessage = `INSERT INTO public.messages (thread_id, sender_id, body) VALUES ($1, $2, $3)`
)

// PGCatalogStore adapts *pgxpool.Pool to VendorStore.
type PGCatalogStore struct{ pool *pgxpool.Pool }

// NewPGCatalogStore wraps the production pool.
func NewPGCatalogStore(pool *pgxpool.Pool) *PGCatalogStore { return &PGCatalogStore{pool: pool} }

func scanProduct(row pgx.Row) (ProductRecord, error) {
	var p ProductRecord
	err := row.Scan(&p.ID, &p.VendorID, &p.CategoryID, &p.Title, &p.Description,
		&p.PriceSat, &p.Stock, &p.ImagePath, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func scanStorefrontProduct(row pgx.Row) (StorefrontProduct, error) {
	var sp StorefrontProduct
	err := row.Scan(&sp.ID, &sp.VendorID, &sp.CategoryID, &sp.Title, &sp.Description,
		&sp.PriceSat, &sp.Stock, &sp.ImagePath, &sp.Status, &sp.CreatedAt, &sp.UpdatedAt,
		&sp.ShopName, &sp.Eta, &sp.VendorUsername)
	return sp, err
}

func (s *PGCatalogStore) CatalogGetVendorByUsername(ctx context.Context, username string) (*VendorRecord, error) {
	v, err := scanVendor(s.pool.QueryRow(ctx, qCatalogVendorByUsername, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: catalog vendor lookup: %w", err)
	}
	return &v, nil
}

func (s *PGCatalogStore) CatalogListCategories(ctx context.Context) ([]CategoryRecord, error) {
	rows, err := s.pool.Query(ctx, qCatalogListCategories)
	if err != nil {
		return nil, fmt.Errorf("store: list categories: %w", err)
	}
	defer rows.Close()
	out := []CategoryRecord{}
	for rows.Next() {
		var c CategoryRecord
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name); err != nil {
			return nil, fmt.Errorf("store: list categories: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list categories: %w", err)
	}
	return out, nil
}

// nullableCat maps -1 ("all") onto a SQL NULL so the shared $1::int filter
// pattern matches ListStorefrontProducts in db/query/catalog.sql.
func nullableCat(categoryID int64) any {
	if categoryID < 0 {
		return nil
	}
	return int32(categoryID)
}

func (s *PGCatalogStore) CatalogListProducts(ctx context.Context, categoryID int64, query string, limit, offset int) ([]StorefrontProduct, error) {
	rows, err := s.pool.Query(ctx, qCatalogListProducts, nullableCat(categoryID), nil, query, int32(limit), int32(offset))
	if err != nil {
		return nil, fmt.Errorf("store: list products: %w", err)
	}
	defer rows.Close()
	out := []StorefrontProduct{}
	for rows.Next() {
		sp, err := scanStorefrontProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list products: scan: %w", err)
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list products: %w", err)
	}
	return out, nil
}

func (s *PGCatalogStore) CatalogCountProducts(ctx context.Context, categoryID int64, query string) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, qCatalogCountProducts, nullableCat(categoryID), query).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count products: %w", err)
	}
	return n, nil
}

func (s *PGCatalogStore) CatalogGetProduct(ctx context.Context, id pgtype.UUID) (*StorefrontProduct, error) {
	sp, err := scanStorefrontProduct(s.pool.QueryRow(ctx, qCatalogGetProduct, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get product: %w", err)
	}
	return &sp, nil
}

func (s *PGCatalogStore) CatalogListVendorProducts(ctx context.Context, vendorUserID pgtype.UUID) ([]ProductRecord, error) {
	rows, err := s.pool.Query(ctx, qCatalogVendorProducts, vendorUserID)
	if err != nil {
		return nil, fmt.Errorf("store: list vendor products: %w", err)
	}
	defer rows.Close()
	out := []ProductRecord{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list vendor products: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list vendor products: %w", err)
	}
	return out, nil
}

func productArgs(p ProductRecord) []any {
	var catCol any
	if p.CategoryID > 0 {
		catCol = int32(p.CategoryID)
	}
	var imgCol any
	if p.ImagePath != "" {
		imgCol = p.ImagePath
	}
	return []any{p.VendorID, catCol, p.Title, p.Description, p.PriceSat, int32(p.Stock), imgCol, p.Status}
}

func (s *PGCatalogStore) CatalogCreateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error) {
	row := s.pool.QueryRow(ctx, qCatalogCreateProduct, productArgs(p)...)
	out, err := scanProduct(row)
	if err != nil {
		return nil, fmt.Errorf("store: create product: %w", err)
	}
	return &out, nil
}

func (s *PGCatalogStore) CatalogUpdateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error) {
	var catCol any
	if p.CategoryID > 0 {
		catCol = int32(p.CategoryID)
	}
	row := s.pool.QueryRow(ctx, qCatalogUpdateProduct, p.ID, p.VendorID, catCol,
		p.Title, p.Description, p.PriceSat, int32(p.Stock), p.Status)
	out, err := scanProduct(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound // unknown id or not owned by this vendor
	}
	if err != nil {
		return nil, fmt.Errorf("store: update product: %w", err)
	}
	return &out, nil
}

func (s *PGCatalogStore) CatalogRemoveProduct(ctx context.Context, productID, vendorUserID pgtype.UUID) error {
	// Resolve the vendor row first: handlers already hold the USER id (session),
	// but the ownership predicate rides the VENDors id.
	var vendorID pgtype.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM public.vendors WHERE user_id = $1`, vendorUserID).Scan(&vendorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: remove product: vendor lookup: %w", err)
	}
	var ignored pgtype.UUID
	err = s.pool.QueryRow(ctx, qCatalogRemoveProduct, productID, vendorID).Scan(&ignored)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: remove product: %w", err)
	}
	return nil
}

func (s *PGCatalogStore) CatalogReviewableLines(ctx context.Context, customerID, vendorID pgtype.UUID) ([]ReviewRecord, error) {
	rows, err := s.pool.Query(ctx, qCatalogReviewableLines, customerID, vendorID)
	if err != nil {
		return nil, fmt.Errorf("store: reviewable lines: %w", err)
	}
	defer rows.Close()
	out := []ReviewRecord{}
	for rows.Next() {
		var rl ReviewRecord
		var hasReview bool
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&rl.ItemID, &rl.ProductID, &rl.ProductTitle, &rl.Qty,
			&rl.OrderStatus, &rl.OrderUpdatedAt, &hasReview, &rl.Rating, &rl.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("store: reviewable lines: scan: %w", err)
		}
		rl.HasReview = hasReview
		rl.ReviewCreatedAt = createdAt.Time
		rl.Editable = time.Since(rl.OrderUpdatedAt.UTC()) <= ReviewEditWindow
		out = append(out, rl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reviewable lines: %w", err)
	}
	return out, nil
}

func (s *PGCatalogStore) CatalogSubmitReview(ctx context.Context, reviewerID pgtype.UUID, itemID int64, rating int, body string) error {
	var ownerID pgtype.UUID
	var status string
	var within bool
	err := s.pool.QueryRow(ctx, qCatalogReviewLineContext, itemID, int64(ReviewEditWindow.Hours())).Scan(&ownerID, &status, &within)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: review line context: %w", err)
	}
	if uuidString(ownerID) != uuidString(reviewerID) {
		return ErrNotFound // only the purchaser of the line may review it
	}
	if status != "shipped" && status != "released" {
		return ErrNotFound // order moved to cancelled/refunded since the form rendered
	}
	if !within {
		return fmt.Errorf("store: review window closed")
	}
	var productID pgtype.UUID
	if err := s.pool.QueryRow(ctx, `SELECT product_id FROM public.order_items WHERE id = $1`, itemID).Scan(&productID); err != nil {
		return fmt.Errorf("store: review product lookup: %w", err)
	}
	if _, err := s.pool.Exec(ctx, qCatalogUpsertReview, itemID, productID, reviewerID, int32(rating), body); err != nil {
		return fmt.Errorf("store: upsert review: %w", err)
	}
	return nil
}

func (s *PGCatalogStore) CatalogRefreshTopVendors(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = DefaultLeaderboardSize
	}
	rows, err := s.pool.Query(ctx, qCatalogTopSelect, int32(limit))
	if err != nil {
		return 0, fmt.Errorf("store: top vendors select: %w", err)
	}
	type snap struct {
		vendorID pgtype.UUID
		sales    int64
		avg      float64
	}
	var list []snap
	for rows.Next() {
		var sv snap
		if err := rows.Scan(&sv.vendorID, &sv.sales, &sv.avg); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: top vendors select: scan: %w", err)
		}
		list = append(list, sv)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("store: top vendors select: %w", err)
	}
	rows.Close()

	// Rewrite atomically: DELETE + INSERTs share one tx so the homepage widget
	// never observes a half-written board.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: top vendors begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if _, err := tx.Exec(ctx, qCatalogDeleteSnapshot); err != nil {
		return 0, fmt.Errorf("store: top vendors delete: %w", err)
	}
	now := time.Now().UTC()
	for i, sv := range list {
		if _, err := tx.Exec(ctx, qCatalogInsertSnapshotRow, int32(i+1), sv.vendorID, int32(sv.sales), sv.avg, now); err != nil {
			return 0, fmt.Errorf("store: top vendors insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("store: top vendors commit: %w", err)
	}
	return len(list), nil
}

func (s *PGCatalogStore) CatalogListTopVendors(ctx context.Context, limit int) ([]TopVendorRow, error) {
	if limit <= 0 {
		limit = DefaultLeaderboardSize
	}
	rows, err := s.pool.Query(ctx, qCatalogListSnapshot, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("store: list top vendors: %w", err)
	}
	defer rows.Close()
	out := []TopVendorRow{}
	for rows.Next() {
		var t TopVendorRow
		if err := rows.Scan(&t.Rank, &t.SalesCount, &t.AvgRating, &t.ShopName, &t.Eta, &t.VendorUsername); err != nil {
			return nil, fmt.Errorf("store: list top vendors: scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list top vendors: %w", err)
	}
	return out, nil
}

func (s *PGCatalogStore) CatalogSendVendorMessage(ctx context.Context, senderID, recipientID pgtype.UUID, body string) error {
	// Find-or-create the unordered participant pair. The INSERT ... ON
	// CONFLICT DO NOTHING RETURNING id pattern closes the race: two concurrent
	// first messages create at most one thread (the CHECK/unique pair makes
	// the loser re-select).
	var threadID pgtype.UUID
	err := s.pool.QueryRow(ctx, qCatalogThreadFindOrCreate, senderID, recipientID).Scan(&threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, qCatalogThreadCreate, senderID, recipientID).Scan(&threadID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict swallowed by DO NOTHING — the row exists now; re-read.
			err = s.pool.QueryRow(ctx, qCatalogThreadFindOrCreate, senderID, recipientID).Scan(&threadID)
		}
	}
	if err != nil {
		return fmt.Errorf("store: vendor message thread: %w", err)
	}
	if _, err := s.pool.Exec(ctx, qCatalogInsertMessage, threadID, senderID, body); err != nil {
		return fmt.Errorf("store: vendor message insert: %w", err)
	}
	return nil
}

var _ pgx.Conn // reserved: pgx typing reference for generated adapters
