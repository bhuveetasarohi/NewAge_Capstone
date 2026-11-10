package main

import (
	"log"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/gateway"
)

func main() {
	gw, err := gateway.New(common.JWTSecret(), common.Env("FRONTEND_DIR", "../frontend"), gateway.Upstreams{
		User:      common.Env("USER_SERVICE_URL", "http://localhost:8001"),
		Product:   common.Env("PRODUCT_SERVICE_URL", "http://localhost:8002"),
		Inventory: common.Env("INVENTORY_SERVICE_URL", "http://localhost:8003"),
		Order:     common.Env("ORDER_SERVICE_URL", "http://localhost:8004"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := common.Serve("gateway", common.Env("LISTEN_ADDR", ":8000"), gw); err != nil {
		log.Fatal(err)
	}
}
