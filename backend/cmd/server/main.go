package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"refunddesk/backend/internal/refunds"
)

func main() {
	dbPath := env("DATABASE_PATH", "./refunddesk.db")
	db, err := refunds.OpenDB(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := refunds.SeedDemoOrders(db); err != nil {
		log.Fatalf("seed demo data: %v", err)
	}
	providers := map[string]refunds.Provider{
		"mock-success": refunds.NewDemoProvider(db, "mock-success"),
		"mock-unknown": refunds.NewDemoProvider(db, "mock-unknown"),
		"mock-fail":    refunds.NewDemoProvider(db, "mock-fail"),
		"mock-a":       refunds.NewDemoProvider(db, "mock-a"),
		"mock-b":       refunds.NewDemoProvider(db, "mock-b"),
	}
	tokens := map[string]refunds.Principal{
		env("TOKEN_MERCHANT_A_WRITE", "demo-a-write"): {MerchantID: "merchant-a", Role: refunds.RoleMerchant},
		env("TOKEN_MERCHANT_B_WRITE", "demo-b-write"): {MerchantID: "merchant-b", Role: refunds.RoleMerchant},
		env("TOKEN_MERCHANT_A_READ", "demo-a-read"):   {MerchantID: "merchant-a", Role: refunds.RoleReadOnly},
	}
	server := &http.Server{Addr: env("ADDR", ":8080"), Handler: refunds.NewHandler(db, providers, tokens), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("refund desk API listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
