package main

import (
	"log"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/product"
)

func main() {
	db, err := common.OpenDB(common.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	svc := &product.Service{DB: db, Secret: common.JWTSecret()}
	if err := common.Serve("product", common.Env("LISTEN_ADDR", ":8002"), svc.Routes()); err != nil {
		log.Fatal(err)
	}
}
