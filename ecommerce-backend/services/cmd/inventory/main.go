package main

import (
	"log"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/inventory"
)

func main() {
	db, err := common.OpenDB(common.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	svc := &inventory.Service{DB: db, Secret: common.JWTSecret()}
	if err := common.Serve("inventory", common.Env("LISTEN_ADDR", ":8003"), svc.Routes()); err != nil {
		log.Fatal(err)
	}
}
