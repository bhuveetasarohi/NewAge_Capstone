package common

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// OrderEventsChannel is the Redis Pub/Sub channel the Python workers subscribe to.
const OrderEventsChannel = "order_events"

// OrderItemEvent is one line of an OrderCreated event.
type OrderItemEvent struct {
	ProductID   int     `json:"product_id"`
	ProductName string  `json:"product_name"`
	Category    string  `json:"category"`
	Quantity    int     `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

// OrderCreatedEvent is published by the Order service after a checkout commits.
type OrderCreatedEvent struct {
	Type      string           `json:"type"`
	OrderID   int              `json:"order_id"`
	UserID    int              `json:"user_id"`
	UserName  string           `json:"user_name"`
	UserEmail string           `json:"user_email"`
	Total     float64          `json:"total_amount"`
	CreatedAt time.Time        `json:"created_at"`
	Items     []OrderItemEvent `json:"items"`
}

// Publisher sends domain events to Redis Pub/Sub.
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher creates a publisher. The connection is established lazily, so the
// service can start even if Redis is still coming up.
func NewPublisher(addr string) *Publisher {
	return &Publisher{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

// PublishOrderCreated broadcasts an OrderCreated event to every subscriber.
func (p *Publisher) PublishOrderCreated(ctx context.Context, ev OrderCreatedEvent) error {
	ev.Type = "OrderCreated"
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return p.rdb.Publish(ctx, OrderEventsChannel, body).Err()
}
