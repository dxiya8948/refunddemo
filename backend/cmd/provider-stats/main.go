package main

import (
	"fmt"
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
	rows, err := db.Query(`SELECT p.provider_channel, p.provider_request_id, p.order_id, p.amount_cents, p.status, p.submit_calls, (SELECT COUNT(*) FROM provider_records q WHERE q.provider_channel = p.provider_channel) FROM provider_records p ORDER BY p.provider_channel, p.created_at`)
	if err != nil {
		log.Fatalf("read provider records: %v", err)
	}
	defer rows.Close()
	fmt.Println("CHANNEL\tREQUEST_ID\tORDER_ID\tAMOUNT_CENTS\tSTATUS\tSUBMIT_CALLS\tACCEPTED_RECORDS")
	for rows.Next() {
		var channel, requestID, orderID, status string
		var amount int64
		var calls, accepted int
		if err := rows.Scan(&channel, &requestID, &orderID, &amount, &status, &calls, &accepted); err != nil {
			log.Fatalf("scan provider record: %v", err)
		}
		fmt.Printf("%s\t%s\t%s\t%d\t%s\t%d\t%d\n", channel, requestID, orderID, amount, status, calls, accepted)
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("read provider records: %v", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
