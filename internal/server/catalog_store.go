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

var ErrNotFound = errors.New("store: not found")

const DefaultLeaderboardSize = 10

type ProductRecord struct {
	ID          pgtype.UUID
	VendorID    pgtype.UUID
	CategoryID  int64
	Title       string
	Description string
	PriceUSDCents    int64
	Stock       int64
	ImagePath   string
	ThumbPath   string // derived public thumbnail path
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type StorefrontProduct struct {
	ProductRecord
	ShopName       string
	Eta            string
	VendorUsername string
}

type CategoryRecord struct {
	ID   int64
	Slug string
	Name string
}

type ReviewRecord struct {
	ItemID          int64
	ProductID       pgtype.UUID
	ProductTitle    string
	Qty             int64
	OrderStatus     string
	OrderUpdatedAt  time.Time
	HasReview       bool
	Rating          int64
	Body            string
	ReviewCreatedAt time.Time
	Editable        bool
}

type TopVendorRow struct {
	Rank           int64
	SalesCount     int64
	AvgRating      float64
	ShopName       string
	Eta            string
	VendorUsername string
}

type VendorMessage struct {
	ID         pgtype.UUID
	SenderID   pgtype.UUID
	Recipient  pgtype.UUID
	Body       string
	CreatedAt  time.Time
	ReadByRecv bool
}

func parseUUIDText(s string) pgtype.UUID {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}
	}
	return u
}

type VendorStore interface {
	CatalogGetVendorByUsername(ctx context.Context, username string) (*VendorRecord, error)
	CatalogListCategories(ctx context.Context) ([]CategoryRecord, error)
	CatalogListProducts(ctx context.Context, categoryID int64, query string, limit, offset int) ([]StorefrontProduct, error)
	CatalogCountProducts(ctx context.Context, categoryID int64, query string) (int64, error)
	CatalogGetProduct(ctx context.Context, id pgtype.UUID) (*StorefrontProduct, error)
	CatalogListVendorProducts(ctx context.Context, vendorUserID pgtype.UUID) ([]ProductRecord, error)
	CatalogCreateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error)
	CatalogUpdateProduct(ctx context.Context, p ProductRecord) (*ProductRecord, error)
	CatalogRemoveProduct(ctx context.Context, productID, vendorUserID pgtype.UUID) error
	CatalogReviewableLines(ctx context.Context, customerID, vendorID pgtype.UUID) ([]ReviewRecord, error)
	CatalogSubmitReview(ctx context.Context, reviewerID pgtype.UUID, itemID int64, rating int, body string) error
	CatalogRefreshTopVendors(ctx context.Context, limit int) (int, error)
	CatalogListTopVendors(ctx context.Context, limit int) ([]TopVendorRow, error)
	CatalogSendVendorMessage(ctx context.Context, senderID, recipientID pgtype.UUID, body string) error
}

const (
	qCatalogVendorByUsername = `SELECT v.id, v.user_id, v.shop_name, v.approved, v.created_at
FROM public.vendors v JOIN public.users u ON u.id = v.user_id
WHERE u.username = $1 AND v.approved`

	qCatalogListCategories = `SELECT id, slug, name FROM public.categories ORDER BY slug`

	// FIXED: Changed $3, $4, $5 to $2, $3, $4 to match the 4 arguments passed.
	qCatalogListProducts = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_usd_cents, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at,
       v.shop_name, v.eta, u.username
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
JOIN public.users u   ON u.id = v.user_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
  AND ($2::text = '' OR to_tsvector('simple', p.title || ' ' || p.description)
        @@ plainto_tsquery('simple', $2))
ORDER BY CASE WHEN $2::text = '' THEN 0 ELSE ts_rank(to_tsvector('simple', p.title || ' ' || p.description),
                 plainto_tsquery('simple', $2)) END DESC,
         p.created_at DESC
LIMIT $3 OFFSET $4`

	qCatalogCountProducts = `SELECT count(*)
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
  AND ($2::text = '' OR to_tsvector('simple', p.title || ' ' || p.description)
        @@ plainto_tsquery('simple', $2))`

	qCatalogGetProduct = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_usd_cents, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at,
       v.shop_name, v.eta, u.username
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
JOIN public.users u   ON u.id = v.user_id
WHERE p.id = $1 AND p.status = 'active' AND v.approved`

	qCatalogVendorProducts = `SELECT p.id, p.vendor_id, COALESCE(p.category_id, 0), p.title, p.description,
       p.price_usd_cents, p.stock, COALESCE(p.image_path, ''), p.status, p.created_at, p.updated_at
FROM public.products p
JOIN public.vendors v ON v.id = p.vendor_id
WHERE v.user_id = $1
ORDER BY p.created_at DESC`

	qCatalogCreateProduct = `INSERT INTO public.products (vendor_id, category_id, title, description, price_usd_cents, stock, image_path, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, vendor_id, COALESCE(category_id, 0), title, description, price_usd_cents, stock,
          COALESCE(image_path, ''), status, created_at, updated_at`

	qCatalogUpdateProduct = `UPDATE public.products SET
    category_id = $3,
    title       = $4,
    description = $5,
    price_usd_cents   = $6,
    stock       = $7,
    status      = $8,
    image_path  = $9,
    updated_at  = now()
WHERE id = $1 AND vendor_id = $2
RETURNING id, vendor_id, COALESCE(category_id, 0), title, description, price_usd_cents, stock,
          COALESCE(image_path, ''), status, created_at, updated_at`

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

	qCatalogThreadFindOrCreate = `SELECT id FROM public.threads
WHERE (participant_a, participant_b) = (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid))`

	qCatalogThreadCreate = `INSERT INTO public.threads (participant_a, participant_b)
VALUES (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid))
ON CONFLICT DO NOTHING
RETURNING id`

	qCatalogInsertMessage = `INSERT INTO public.messages (thread_id, sender_id, body) VALUES ($1, $2, $3)`
)

type PGCatalogStore struct{ pool *pgxpool.Pool }

func NewPGCatalogStore(pool *pgxpool.Pool) *PGCatalogStore { return &PGCatalogStore{pool: pool} }

func scanProduct(row pgx.Row) (ProductRecord, error) {
	var p ProductRecord
	err := row.Scan(&p.ID, &p.VendorID, &p.CategoryID, &p.Title, &p.Description,
		&p.PriceUSDCents, &p.Stock, &p.ImagePath, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	p.ThumbPath = deriveThumb(p.ImagePath)
	return p, err
}

func scanStorefrontProduct(row pgx.Row) (StorefrontProduct, error) {
	var sp StorefrontProduct
	err := row.Scan(&sp.ID, &sp.VendorID, &sp.CategoryID, &sp.Title, &sp.Description,
		&sp.PriceUSDCents, &sp.Stock, &sp.ImagePath, &sp.Status, &sp.CreatedAt, &sp.UpdatedAt,
		&sp.ShopName, &sp.Eta, &sp.VendorUsername)
	sp.ThumbPath = deriveThumb(sp.ImagePath)
	return sp, err
}

// Note: scanVendor is already defined in auth_handlers.go, so it is omitted here.

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

func nullableCat(categoryID int64) any {
	if categoryID < 0 {
		return nil
	}
	return int32(categoryID)
}

func (s *PGCatalogStore) CatalogListProducts(ctx context.Context, categoryID int64, query string, limit, offset int) ([]StorefrontProduct, error) {
	// FIXED: Removed the stray 'nil' argument. Now passes exactly 4 arguments matching $1, $2, $3, $4.
	rows, err := s.pool.Query(ctx, qCatalogListProducts, nullableCat(categoryID), query, int32(limit), int32(offset))
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
	s.populateRealETA(ctx, out)
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
	return []any{p.VendorID, catCol, p.Title, p.Description, p.PriceUSDCents, int32(p.Stock), imgCol, p.Status}
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
	var imgCol any
	if p.ImagePath != "" {
		imgCol = p.ImagePath
	}
	row := s.pool.QueryRow(ctx, qCatalogUpdateProduct, p.ID, p.VendorID, catCol,
		p.Title, p.Description, p.PriceUSDCents, int32(p.Stock), p.Status, imgCol)
	out, err := scanProduct(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update product: %w", err)
	}
	return &out, nil
}

func (s *PGCatalogStore) CatalogRemoveProduct(ctx context.Context, productID, vendorUserID pgtype.UUID) error {
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
		return ErrNotFound
	}
	if status != "shipped" && status != "released" {
		return ErrNotFound
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: top vendors begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	var threadID pgtype.UUID
	err := s.pool.QueryRow(ctx, qCatalogThreadFindOrCreate, senderID, recipientID).Scan(&threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, qCatalogThreadCreate, senderID, recipientID).Scan(&threadID)
		if errors.Is(err, pgx.ErrNoRows) {
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

// calculateRealDispatchETA computes average dispatch time from order history.
func (s *PGCatalogStore) calculateRealDispatchETA(ctx context.Context, vendorID pgtype.UUID) (string, error) {
	var avgHours float64
	err := s.pool.QueryRow(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (o.shipped_at - o.created_at)) / 3600)
		FROM public.orders o
		WHERE o.vendor_id = $1
		  AND o.status IN ('shipped', 'released')
		  AND o.shipped_at IS NOT NULL
	`, vendorID).Scan(&avgHours)
	if err != nil || avgHours <= 0 {
		return "", nil
	}
	switch {
	case avgHours < 24:
		return fmt.Sprintf("%dh dispatch", int(avgHours)), nil
	case avgHours < 168:
		return fmt.Sprintf("%dd dispatch", int(avgHours/24)), nil
	default:
		return fmt.Sprintf("%dw dispatch", int(avgHours/168)), nil
	}
}

// populateRealETA overrides vendor-stated Eta with calculated metrics (cached per vendor).
func (s *PGCatalogStore) populateRealETA(ctx context.Context, products []StorefrontProduct) {
	vendorCache := make(map[pgtype.UUID]string)
	for i := range products {
		vid := products[i].VendorID
		if eta, ok := vendorCache[vid]; ok {
			if eta != "" { products[i].Eta = eta }
			continue
		}
		realETA, err := s.calculateRealDispatchETA(ctx, vid)
		if err == nil && realETA != "" {
			vendorCache[vid] = realETA
			products[i].Eta = realETA
		} else {
			vendorCache[vid] = ""
			switch products[i].Eta {
			case "low":
				products[i].Eta = "ships fast (vendor est.)"
			case "medium":
				products[i].Eta = "ships standard (vendor est.)"
			case "high":
				products[i].Eta = "ships slowly (vendor est.)"
			}
		}
	}
}


// ---- WithdrawalStore implementation (ledger-backed) ----
func (s *PGCatalogStore) GetVendorBalanceSat(ctx context.Context, vendorID pgtype.UUID) (int64, error) {
	return VendorBalancePiconero(ctx, s.pool, vendorID), nil
}
func (s *PGCatalogStore) CreateWithdrawalRequest(ctx context.Context, vendorID pgtype.UUID, amount int64, address string) error {
	bal := VendorBalancePiconero(ctx, s.pool, vendorID)
	var pending int64
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_piconero),0) FROM withdrawals WHERE vendor_id=$1 AND status IN ('pending','approved')`, vendorID).Scan(&pending)
	if amount <= 0 || amount > bal-pending {
		return fmt.Errorf("insufficient available balance")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO withdrawals (vendor_id, amount_piconero, xmr_address, status) VALUES ($1,$2,$3,'pending')`, vendorID, amount, address)
	return err
}
func (s *PGCatalogStore) ListWithdrawalsByVendor(ctx context.Context, vendorID pgtype.UUID) ([]WithdrawalRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, vendor_id, amount_piconero, xmr_address, status, created_at FROM withdrawals WHERE vendor_id=$1 ORDER BY created_at DESC`, vendorID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []WithdrawalRecord
	for rows.Next() { var w WithdrawalRecord; if rows.Scan(&w.ID,&w.VendorID,&w.AmountPiconero,&w.XmrAddress,&w.Status,&w.CreatedAt)==nil { out=append(out,w) } }
	return out, rows.Err()
}
func (s *PGCatalogStore) ListPendingWithdrawals(ctx context.Context) ([]WithdrawalRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, vendor_id, amount_piconero, xmr_address, status, created_at FROM withdrawals WHERE status IN ('pending','approved') ORDER BY created_at`)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []WithdrawalRecord
	for rows.Next() { var w WithdrawalRecord; if rows.Scan(&w.ID,&w.VendorID,&w.AmountPiconero,&w.XmrAddress,&w.Status,&w.CreatedAt)==nil { out=append(out,w) } }
	return out, rows.Err()
}
func (s *PGCatalogStore) GetWithdrawal(ctx context.Context, id pgtype.UUID) (*WithdrawalRecord, error) {
	var w WithdrawalRecord
	err := s.pool.QueryRow(ctx, `SELECT id, vendor_id, amount_piconero, xmr_address, status, created_at FROM withdrawals WHERE id=$1`, id).Scan(&w.ID,&w.VendorID,&w.AmountPiconero,&w.XmrAddress,&w.Status,&w.CreatedAt)
	if err != nil { return nil, err }
	return &w, nil
}
func (s *PGCatalogStore) ReviewWithdrawal(ctx context.Context, id, reviewer pgtype.UUID, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE withdrawals SET status=$2, reviewed_by=$3, reviewed_at=now() WHERE id=$1`, id, status, reviewer)
	return err
}
