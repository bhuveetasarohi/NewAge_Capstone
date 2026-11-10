// Package product manages the catalogue and serves recommendations built from past orders.
package product

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"ecommerce-backend/services/internal/common"
)

// Service holds the dependencies of the product service.
type Service struct {
	DB     *sql.DB
	Secret []byte
}

// Product is a catalogue entry with its current stock level.
type Product struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Category    string  `json:"category"`
	Emoji       string  `json:"emoji"`
	Stock       int     `json:"stock"`
}

// Recommendation is a product that is often bought together with the user's past items.
type Recommendation struct {
	Product
	Score int `json:"score"`
}

type productInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Category    string  `json:"category"`
	Emoji       string  `json:"emoji"`
	Stock       int     `json:"stock"`
}

// validate normalises the input and returns a human-readable error, or "" when valid.
func (in *productInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Category = strings.ToLower(strings.TrimSpace(in.Category))
	in.Emoji = strings.TrimSpace(in.Emoji)
	if in.Emoji == "" {
		in.Emoji = "📦"
	}
	switch {
	case in.Name == "" || len(in.Name) > 150:
		return "name is required (max 150 characters)"
	case in.Category == "" || len(in.Category) > 50:
		return "category is required (max 50 characters)"
	case len(in.Description) > 1000:
		return "description is too long (max 1000 characters)"
	case in.Price < 0 || in.Price > 10000000:
		return "price must be between 0 and 10,000,000"
	case in.Stock < 0:
		return "stock cannot be negative"
	}
	return ""
}

// Routes returns the HTTP handler for this service.
func (s *Service) Routes() http.Handler {
	auth := func(h http.HandlerFunc) http.Handler { return common.Require(s.Secret, h) }

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/products", s.list)
	mux.HandleFunc("GET /api/products/{id}", s.get)
	mux.HandleFunc("GET /api/categories", s.categories)
	mux.Handle("POST /api/products", auth(s.create))
	mux.Handle("PUT /api/products/{id}", auth(s.update))
	mux.Handle("GET /api/recommendations", auth(s.recommendations))
	mux.Handle("GET /health", common.Health("product"))
	return mux
}

const productSelect = `
	SELECT p.id, p.name, p.description, p.price, p.category, p.image_emoji, COALESCE(i.stock_quantity, 0)
	  FROM products p
	  LEFT JOIN inventory i ON i.product_id = p.id`

type scanner interface {
	Scan(dest ...any) error
}

func scanProduct(s scanner, p *Product) error {
	return s.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Category, &p.Emoji, &p.Stock)
}

func (s *Service) fetch(ctx context.Context, id int) (Product, error) {
	var p Product
	err := scanProduct(s.DB.QueryRowContext(ctx, productSelect+` WHERE p.id = $1`, id), &p)
	return p, err
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	category := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(search) > 100 {
		common.WriteError(w, http.StatusBadRequest, "search term is too long")
		return
	}

	rows, err := s.DB.QueryContext(r.Context(), productSelect+`
		 WHERE ($1 = '' OR p.category = $1)
		   AND ($2 = '' OR p.name ILIKE '%' || $2 || '%' OR p.description ILIKE '%' || $2 || '%')
		 ORDER BY p.id`, category, search)
	if err != nil {
		common.ServerError(w, "list products", err)
		return
	}
	defer rows.Close()

	products := []Product{}
	for rows.Next() {
		var p Product
		if err := scanProduct(rows, &p); err != nil {
			common.ServerError(w, "scan product", err)
			return
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		common.ServerError(w, "iterate products", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, products)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "id")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	p, err := s.fetch(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		common.ServerError(w, "get product", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) categories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT DISTINCT category FROM products ORDER BY category`)
	if err != nil {
		common.ServerError(w, "list categories", err)
		return
	}
	defer rows.Close()

	cats := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			common.ServerError(w, "scan category", err)
			return
		}
		cats = append(cats, c)
	}
	common.WriteJSON(w, http.StatusOK, cats)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var in productInput
	if err := common.DecodeJSON(w, r, &in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := in.validate(); msg != "" {
		common.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	ctx := r.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		common.ServerError(w, "begin create product", err)
		return
	}
	defer tx.Rollback()

	var id int
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO products (name, description, price, category, image_emoji)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.Name, in.Description, in.Price, in.Category, in.Emoji,
	).Scan(&id); err != nil {
		common.ServerError(w, "insert product", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO inventory (product_id, stock_quantity) VALUES ($1, $2)`, id, in.Stock); err != nil {
		common.ServerError(w, "insert inventory", err)
		return
	}
	if err := tx.Commit(); err != nil {
		common.ServerError(w, "commit product", err)
		return
	}

	p, err := s.fetch(ctx, id)
	if err != nil {
		common.ServerError(w, "reload product", err)
		return
	}
	common.WriteJSON(w, http.StatusCreated, p)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "id")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var in productInput
	if err := common.DecodeJSON(w, r, &in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := in.validate(); msg != "" {
		common.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	res, err := s.DB.ExecContext(r.Context(),
		`UPDATE products SET name = $2, description = $3, price = $4, category = $5, image_emoji = $6
		  WHERE id = $1`,
		id, in.Name, in.Description, in.Price, in.Category, in.Emoji)
	if err != nil {
		common.ServerError(w, "update product", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	// Stock is managed by the inventory service, so it is not changed here.
	p, err := s.fetch(r.Context(), id)
	if err != nil {
		common.ServerError(w, "reload product", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, p)
}

// recommendations finds products that appear in the same orders as the user's past
// purchases. The Python recommendation worker keeps product_copurchases up to date.
func (s *Service) recommendations(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.CurrentUser(r)
	rows, err := s.DB.QueryContext(r.Context(), `
		WITH bought AS (
		    SELECT DISTINCT oi.product_id
		      FROM order_items oi
		      JOIN orders o ON o.id = oi.order_id
		     WHERE o.user_id = $1
		)
		SELECT p.id, p.name, p.description, p.price, p.category, p.image_emoji,
		       COALESCE(i.stock_quantity, 0), SUM(c.times_bought_together) AS score
		  FROM product_copurchases c
		  JOIN bought b ON b.product_id = c.product_id
		  JOIN products p ON p.id = c.related_product_id
		  LEFT JOIN inventory i ON i.product_id = p.id
		 WHERE c.related_product_id NOT IN (SELECT product_id FROM bought)
		 GROUP BY p.id, i.stock_quantity
		 ORDER BY score DESC, p.name
		 LIMIT 6`, claims.Sub)
	if err != nil {
		common.ServerError(w, "recommendations", err)
		return
	}
	defer rows.Close()

	recs := []Recommendation{}
	for rows.Next() {
		var rec Recommendation
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Description, &rec.Price,
			&rec.Category, &rec.Emoji, &rec.Stock, &rec.Score); err != nil {
			common.ServerError(w, "scan recommendation", err)
			return
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		common.ServerError(w, "iterate recommendations", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, map[string]any{"recommendations": recs})
}
