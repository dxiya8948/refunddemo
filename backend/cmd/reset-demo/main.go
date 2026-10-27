package main

import (
	"log"
	"os"

	"refunddesk/backend/internal/refunds"
)

func main() {
	db, err := refunds.OpenDB(env("DATABASE_PATH", "./refunddesk.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := refunds.ResetDemoData(db); err != nil {
		log.Fatalf("reset demo data: %v", err)
	}
	if err := refunds.SeedDemoOrders(db); err != nil {
		log.Fatalf("seed demo orders: %v", err)
	}
	log.Print("demo orders, refunds, and mock channel records reset")
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
