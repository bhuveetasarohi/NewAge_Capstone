// Package order runs the cart and the synchronous checkout. Checkout is the only
// critical path: it commits the order and reserves stock in one transaction, then
// publishes OrderCreated so the Python workers can do the slower background work.
package order

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/inventory"
)

const maxQtyPerLine = 99

// Service holds the dependencies of the order service.
type Service struct {
	DB         *sql.DB
	Secret     []byte
	Publisher  *common.Publisher
	InvoiceDir string
}

// CartItem is one line in the shopping cart.
type CartItem struct {
	ProductID int     `json:"product_id"`
	Name      string  `json:"name"`
	Category  string  `json:"category"`
	Emoji     string  `json:"emoji"`
	Price     float64 `json:"price"`
	Quantity  int     `json:"quantity"`
	Stock     int     `json:"stock"`
	LineTotal float64 `json:"line_total"`
}

// Cart is the full response for the cart endpoints.
type Cart struct {
	Items     []CartItem `json:"items"`
	ItemCount int        `json:"item_count"`
	Total     float64    `json:"total"`
}

// OrderLine is one purchased item on an order.
type OrderLine struct {
	ProductID int     `json:"product_id"`
	Name      string  `json:"product_name"`
	Category  string  `json:"category"`
	Quantity  int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	LineTotal float64 `json:"line_total"`
}

// Order is a placed order with its items.
type Order struct {
	ID            int         `json:"id"`
	Total         float64     `json:"total_amount"`
	Status        string      `json:"status"`
	PaymentStatus string      `json:"payment_status"`
	CreatedAt     time.Time   `json:"created_at"`
	Items         []OrderLine `json:"items"`
}

// Routes returns the HTTP handler for this service.
func (s *Service) Routes() http.Handler {
	auth := func(h http.HandlerFunc) http.Handler { return common.Require(s.Secret, h) }

	mux := http.NewServeMux()
	mux.Handle("GET /api/cart", auth(s.getCart))
	mux.Handle("POST /api/cart", auth(s.addToCart))
	mux.Handle("PUT /api/cart/{productId}", auth(s.setCartQuantity))
	mux.Handle("DELETE /api/cart/{productId}", auth(s.removeFromCart))
	mux.Handle("POST /api/checkout", auth(s.checkout))
	mux.Handle("GET /api/orders", auth(s.listOrders))
	mux.Handle("GET /api/orders/{id}", auth(s.getOrder))
	mux.Handle("GET /api/orders/{id}/invoice", auth(s.invoice))
	mux.Handle("GET /health", common.Health("order"))
	return mux
}

func userID(r *http.Request) int {
	c, _ := common.CurrentUser(r)
	return c.Sub
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ---------------------------------------------------------------- cart

func (s *Service) loadCart(ctx context.Context, uid int) (Cart, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT p.id, p.name, p.category, p.image_emoji, p.price, c.quantity, COALESCE(i.stock_quantity, 0)
		  FROM cart_items c
		  JOIN products p ON p.id = c.product_id
		  LEFT JOIN inventory i ON i.product_id = p.id
		 WHERE c.user_id = $1
		 ORDER BY p.id`, uid)
	if err != nil {
		return Cart{}, err
	}
	defer rows.Close()

	cart := Cart{Items: []CartItem{}}
	for rows.Next() {
		var it CartItem
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Category, &it.Emoji, &it.Price, &it.Quantity, &it.Stock); err != nil {
			return Cart{}, err
		}
		it.LineTotal = round2(it.Price * float64(it.Quantity))
		cart.Items = append(cart.Items, it)
		cart.ItemCount += it.Quantity
		cart.Total += it.LineTotal
	}
	if err := rows.Err(); err != nil {
		return Cart{}, err
	}
	cart.Total = round2(cart.Total)
	return cart, nil
}

func (s *Service) respondCart(w http.ResponseWriter, r *http.Request, status int) {
	cart, err := s.loadCart(r.Context(), userID(r))
	if err != nil {
		common.ServerError(w, "load cart", err)
		return
	}
	common.WriteJSON(w, status, cart)
}

func (s *Service) stockFor(ctx context.Context, productID int) (int, error) {
	var stock int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(i.stock_quantity, 0)
		  FROM products p
		  LEFT JOIN inventory i ON i.product_id = p.id
		 WHERE p.id = $1`, productID).Scan(&stock)
	return stock, err
}

func (s *Service) getCart(w http.ResponseWriter, r *http.Request) {
	s.respondCart(w, r, http.StatusOK)
}

func (s *Service) addToCart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID int `json:"product_id"`
		Quantity  int `json:"quantity"`
	}
	if err := common.DecodeJSON(w, r, &in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.Quantity == 0 {
		in.Quantity = 1
	}
	if in.ProductID <= 0 || in.Quantity < 1 || in.Quantity > maxQtyPerLine {
		common.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("product_id is required and quantity must be between 1 and %d", maxQtyPerLine))
		return
	}

	ctx := r.Context()
	uid := userID(r)
	stock, err := s.stockFor(ctx, in.ProductID)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		common.ServerError(w, "stock lookup", err)
		return
	}

	var inCart int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM cart_items WHERE user_id = $1 AND product_id = $2`,
		uid, in.ProductID).Scan(&inCart); err != nil {
		common.ServerError(w, "cart lookup", err)
		return
	}
	// This is an early, friendly check. Checkout re-checks and reserves stock atomically.
	if inCart+in.Quantity > stock {
		common.WriteError(w, http.StatusConflict, stockMessage(stock, inCart))
		return
	}

	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO cart_items (user_id, product_id, quantity) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, product_id)
		DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity`,
		uid, in.ProductID, in.Quantity); err != nil {
		common.ServerError(w, "add to cart", err)
		return
	}
	s.respondCart(w, r, http.StatusOK)
}

func (s *Service) setCartQuantity(w http.ResponseWriter, r *http.Request) {
	pid, ok := common.PathInt(r, "productId")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var in struct {
		Quantity *int `json:"quantity"`
	}
	if err := common.DecodeJSON(w, r, &in); err != nil || in.Quantity == nil {
		common.WriteError(w, http.StatusBadRequest, "quantity is required")
		return
	}
	qty := *in.Quantity
	if qty < 0 || qty > maxQtyPerLine {
		common.WriteError(w, http.StatusBadRequest, fmt.Sprintf("quantity must be between 0 and %d", maxQtyPerLine))
		return
	}

	ctx := r.Context()
	uid := userID(r)
	if qty == 0 {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM cart_items WHERE user_id = $1 AND product_id = $2`, uid, pid); err != nil {
			common.ServerError(w, "remove cart item", err)
			return
		}
		s.respondCart(w, r, http.StatusOK)
		return
	}

	stock, err := s.stockFor(ctx, pid)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		common.ServerError(w, "stock lookup", err)
		return
	}
	if qty > stock {
		common.WriteError(w, http.StatusConflict, fmt.Sprintf("only %d unit(s) in stock", stock))
		return
	}
	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO cart_items (user_id, product_id, quantity) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, product_id) DO UPDATE SET quantity = EXCLUDED.quantity`,
		uid, pid, qty); err != nil {
		common.ServerError(w, "set cart quantity", err)
		return
	}
	s.respondCart(w, r, http.StatusOK)
}

func (s *Service) removeFromCart(w http.ResponseWriter, r *http.Request) {
	pid, ok := common.PathInt(r, "productId")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	if _, err := s.DB.ExecContext(r.Context(),
		`DELETE FROM cart_items WHERE user_id = $1 AND product_id = $2`, userID(r), pid); err != nil {
		common.ServerError(w, "remove from cart", err)
		return
	}
	s.respondCart(w, r, http.StatusOK)
}

func stockMessage(stock, inCart int) string {
	remaining := stock - inCart
	switch {
	case remaining > 0:
		return fmt.Sprintf("only %d more unit(s) available", remaining)
	case inCart > 0:
		return fmt.Sprintf("you already have all %d available unit(s) in your cart", inCart)
	default:
		return "this product is out of stock"
	}
}

// ---------------------------------------------------------------- checkout

type cartLine struct {
	productID int
	qty       int
	name      string
	category  string
	price     float64
}

// lockCart reads the user's cart rows and locks them, so a double-clicked checkout
// cannot place two orders from the same cart.
func lockCart(ctx context.Context, tx *sql.Tx, uid int) ([]cartLine, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.product_id, c.quantity, p.name, p.category, p.price
		  FROM cart_items c
		  JOIN products p ON p.id = c.product_id
		 WHERE c.user_id = $1
		 ORDER BY c.product_id
		   FOR UPDATE OF c`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []cartLine
	for rows.Next() {
		var l cartLine
		if err := rows.Scan(&l.productID, &l.qty, &l.name, &l.category, &l.price); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func (s *Service) checkout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := userID(r)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		common.ServerError(w, "begin checkout", err)
		return
	}
	defer tx.Rollback() // no-op once committed; undoes everything on any early return

	lines, err := lockCart(ctx, tx, uid)
	if err != nil {
		common.ServerError(w, "lock cart", err)
		return
	}
	if len(lines) == 0 {
		common.WriteError(w, http.StatusBadRequest, "your cart is empty")
		return
	}

	var total float64
	reserve := make([]inventory.Line, 0, len(lines))
	for _, l := range lines {
		total += l.price * float64(l.qty)
		reserve = append(reserve, inventory.Line{ProductID: l.productID, Quantity: l.qty})
	}
	total = round2(total)

	if err := inventory.Reserve(ctx, tx, reserve); err != nil {
		var stockErr *inventory.StockError
		if errors.As(err, &stockErr) {
			common.WriteError(w, http.StatusConflict,
				fmt.Sprintf("not enough stock for %q. Please update your cart and try again.", nameOf(lines, stockErr.ProductID)))
			return
		}
		common.ServerError(w, "reserve stock", err)
		return
	}

	var name, email string
	if err := tx.QueryRowContext(ctx, `SELECT name, email FROM users WHERE id = $1`, uid).Scan(&name, &email); err != nil {
		common.ServerError(w, "load customer", err)
		return
	}

	var orderID int
	var createdAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO orders (user_id, total_amount, status, payment_status)
		VALUES ($1, $2, 'CONFIRMED', 'PAID_SIMULATED')
		RETURNING id, created_at`, uid, total).Scan(&orderID, &createdAt); err != nil {
		common.ServerError(w, "insert order", err)
		return
	}

	event := common.OrderCreatedEvent{
		OrderID:   orderID,
		UserID:    uid,
		UserName:  name,
		UserEmail: email,
		Total:     total,
		CreatedAt: createdAt,
	}
	for _, l := range lines {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO order_items (order_id, product_id, product_name, category, quantity, unit_price)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			orderID, l.productID, l.name, l.category, l.qty, l.price); err != nil {
			common.ServerError(w, "insert order item", err)
			return
		}
		event.Items = append(event.Items, common.OrderItemEvent{
			ProductID:   l.productID,
			ProductName: l.name,
			Category:    l.category,
			Quantity:    l.qty,
			UnitPrice:   l.price,
		})
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM cart_items WHERE user_id = $1`, uid); err != nil {
		common.ServerError(w, "clear cart", err)
		return
	}
	if err := tx.Commit(); err != nil {
		common.ServerError(w, "commit checkout", err)
		return
	}

	// The order is durable now. Announce it. If the publish fails, the order still stands;
	// the background work is simply skipped for this order and logged here.
	pubCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Publisher.PublishOrderCreated(pubCtx, event); err != nil {
		log.Printf("WARNING: order %d committed but OrderCreated publish failed: %v", orderID, err)
	}

	orders, err := s.loadOrders(ctx, uid, orderID)
	if err != nil || len(orders) == 0 {
		common.WriteJSON(w, http.StatusCreated, map[string]any{
			"message":  "Order placed. Confirmation and invoice are being processed in the background.",
			"order_id": orderID,
		})
		return
	}
	common.WriteJSON(w, http.StatusCreated, map[string]any{
		"message": "Order placed. Confirmation and invoice are being processed in the background.",
		"order":   orders[0],
	})
}

func nameOf(lines []cartLine, productID int) string {
	for _, l := range lines {
		if l.productID == productID {
			return l.name
		}
	}
	return fmt.Sprintf("product %d", productID)
}

// ---------------------------------------------------------------- orders

// loadOrders returns the user's orders (newest first). orderID = 0 means all orders.
func (s *Service) loadOrders(ctx context.Context, uid, orderID int) ([]Order, error) {
	orders, index, err := s.loadOrderHeaders(ctx, uid, orderID)
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT oi.order_id, oi.product_id, oi.product_name, oi.category, oi.quantity, oi.unit_price
		  FROM order_items oi
		  JOIN orders o ON o.id = oi.order_id
		 WHERE o.user_id = $1 AND ($2 = 0 OR o.id = $2)
		 ORDER BY oi.id`, uid, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ref int
		var line OrderLine
		if err := rows.Scan(&ref, &line.ProductID, &line.Name, &line.Category, &line.Quantity, &line.UnitPrice); err != nil {
			return nil, err
		}
		line.LineTotal = round2(line.UnitPrice * float64(line.Quantity))
		if i, ok := index[ref]; ok {
			orders[i].Items = append(orders[i].Items, line)
		}
	}
	return orders, rows.Err()
}

func (s *Service) loadOrderHeaders(ctx context.Context, uid, orderID int) ([]Order, map[int]int, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, total_amount, status, payment_status, created_at
		  FROM orders
		 WHERE user_id = $1 AND ($2 = 0 OR id = $2)
		 ORDER BY id DESC
		 LIMIT 50`, uid, orderID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	orders := []Order{}
	index := map[int]int{}
	for rows.Next() {
		o := Order{Items: []OrderLine{}}
		if err := rows.Scan(&o.ID, &o.Total, &o.Status, &o.PaymentStatus, &o.CreatedAt); err != nil {
			return nil, nil, err
		}
		index[o.ID] = len(orders)
		orders = append(orders, o)
	}
	return orders, index, rows.Err()
}

func (s *Service) listOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.loadOrders(r.Context(), userID(r), 0)
	if err != nil {
		common.ServerError(w, "list orders", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, orders)
}

func (s *Service) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "id")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	orders, err := s.loadOrders(r.Context(), userID(r), id)
	if err != nil {
		common.ServerError(w, "get order", err)
		return
	}
	if len(orders) == 0 {
		common.WriteError(w, http.StatusNotFound, "order not found")
		return
	}
	common.WriteJSON(w, http.StatusOK, orders[0])
}

// invoice serves the PDF written by the Python invoice worker. The file appears a moment
// after checkout, so until then the endpoint answers 202 and the client retries.
func (s *Service) invoice(w http.ResponseWriter, r *http.Request) {
	id, ok := common.PathInt(r, "id")
	if !ok {
		common.WriteError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	uid := userID(r)

	var owned bool
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND user_id = $2)`, id, uid).Scan(&owned); err != nil {
		common.ServerError(w, "check order owner", err)
		return
	}
	if !owned {
		common.WriteError(w, http.StatusNotFound, "order not found")
		return
	}

	path := filepath.Join(s.InvoiceDir, fmt.Sprintf("order_%d.pdf", id))
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			common.WriteJSON(w, http.StatusAccepted, map[string]string{
				"status":  "pending",
				"message": "Invoice is still being generated. Please retry in a moment.",
			})
			return
		}
		common.ServerError(w, "stat invoice", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="invoice_%d.pdf"`, id))
	http.ServeFile(w, r, path)
}
