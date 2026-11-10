package main

import (
	"log"

	"ecommerce-backend/services/internal/common"
	"ecommerce-backend/services/internal/user"
)

func main() {
	db, err := common.OpenDB(common.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	svc := &user.Service{DB: db, Secret: common.JWTSecret()}
	if err := common.Serve("user", common.Env("LISTEN_ADDR", ":8001"), svc.Routes()); err != nil {
		log.Fatal(err)
	}
}
