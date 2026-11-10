package main

import (
	"log"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/order"
)

func main() {
	db, err := common.OpenDB(common.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	svc := &order.Service{
		DB:         db,
		Secret:     common.JWTSecret(),
		Publisher:  common.NewPublisher(common.RedisAddr()),
		InvoiceDir: common.Env("INVOICE_DIR", "./invoices"),
	}
	if err := common.Serve("order", common.Env("LISTEN_ADDR", ":8004"), svc.Routes()); err != nil {
		log.Fatal(err)
	}
}
