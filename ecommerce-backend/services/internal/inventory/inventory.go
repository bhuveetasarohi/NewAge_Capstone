// Package inventory owns stock levels. Reserve is shared with the order service so that
// stock is checked and decremented inside the same database transaction as the order.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"ecommerce-backend/services/internal/common"
)

// Service serves the inventory endpoints.
type Service struct {
	DB     *sql.DB
	Secret []byte
}

// Level is the stock position of one product.
type Level struct {
	ProductID     int       `json:"product_id"`
	ProductName   string    `json:"product_name"`
	StockQuantity int       `json:"stock_quantity"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Line is one product and quantity to reserve.
type Line struct {
	ProductID int
	Quantity  int
}

// StockError reports the product that could not be reserved in the requested quantity.
type StockError struct {
	ProductID int
}

func (e *StockError) Error() string {
	return fmt.Sprintf("insufficient stock for product %d", e.ProductID)
}

// Reserve decrements stock for every line inside the caller's transaction. Callers must
// pass lines sorted by ProductID so concurrent checkouts lock rows in the same order.
//
// The UPDATE only succeeds while enough units remain. Postgres row locks serialise
// competing checkouts, so overselling cannot happen even under heavy concurrency.
func Reserve(ctx context.Context, tx *sql.Tx, lines []Line) error {
	for _, l := range lines {
		res, err := tx.ExecContext(ctx, `
			UPDATE inventory
			   SET stock_quantity = stock_quantity - $2, updated_at = NOW()
			 WHERE product_id = $1 AND stock_quantity >= $2`,
			l.ProductID, l.Quantity)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &StockError{ProductID: l.ProductID}
		}
	}
	return nil
}

// Routes returns the HTTP handler for this service.
func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/inventory", s.list)
	mux.HandleFunc("GET /api/inventory/{productId}", s.get)
	mux.Handle("PUT /api/inventory/{productId}", common.Require(s.Secret, http.HandlerFunc(s.set)))
	mux.Handle("GET /health", common.Health("inventory"))
	return mux
}

const levelSelect = `
	SELECT i.product_id, p.name, i.stock_quantity, i.updated_at
	  FROM inventory i
	  JOIN products p ON p.id = i.product_id`

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), levelSelect+` ORDER BY i.product_id`)
	if err != nil {
		common.ServerError(w, "list inventory", err)
		return
	}
	defer rows.Close()

	levels := []Level{}
	for rows.Next() {
		var l Level
		if err := rows.Scan(&l.ProductID, &l.ProductName, &l.StockQuantity, &l.UpdatedAt); err != nil {
			common.ServerError(w, "scan inventory", err)
			return
		}
		levels = append(levels, l)
	}
	if err := rows.Err(); err != nil {
		common.ServerError(w, "iterate inventory", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, levels)
}

func (s *Service) level(ctx context.Context, productID int) (Level, error) {
	var l Level
	err := s.DB.QueryRowContext(ctx, levelSelect+` WHERE i.product_id = $1`, productID).
		Scan(&l.ProductID, &l.ProductName, &l.StockQuantity, &l.UpdatedAt)
	return l, err
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "productId")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	l, err := s.level(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		common.ServerError(w, "get inventory", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, l)
}

func (s *Service) set(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "productId")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var in struct {
		StockQuantity *int `json:"stock_quantity"`
	}
	if err := common.DecodeJSON(w, r, &in); err != nil || in.StockQuantity == nil {
		common.WriteError(w, http.StatusBadRequest, "stock_quantity is required")
		return
	}
	if *in.StockQuantity < 0 || *in.StockQuantity > 1000000 {
		common.WriteError(w, http.StatusBadRequest, "stock_quantity must be between 0 and 1,000,000")
		return
	}

	// Upsert so a product that somehow has no inventory row still gets one.
	var productID int
	err := s.DB.QueryRowContext(r.Context(), `
		INSERT INTO inventory (product_id, stock_quantity)
		SELECT id, $2::int FROM products WHERE id = $1
		ON CONFLICT (product_id)
		DO UPDATE SET stock_quantity = EXCLUDED.stock_quantity, updated_at = NOW()
		RETURNING product_id`, id, *in.StockQuantity).Scan(&productID)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		common.ServerError(w, "set inventory", err)
		return
	}
	l, err := s.level(r.Context(), productID)
	if err != nil {
		common.ServerError(w, "reload inventory", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, l)
}
