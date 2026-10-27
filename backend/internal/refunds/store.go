package refunds

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS orders (
  id TEXT PRIMARY KEY,
  merchant_id TEXT NOT NULL,
  amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
  currency TEXT NOT NULL,
  status TEXT NOT NULL,
  provider_channel TEXT NOT NULL,
  payment_channel_status TEXT NOT NULL DEFAULT 'UNKNOWN'
);
CREATE TABLE IF NOT EXISTS refunds (
  id TEXT PRIMARY KEY,
  order_id TEXT NOT NULL UNIQUE REFERENCES orders(id),
  merchant_id TEXT NOT NULL,
  amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
  currency TEXT NOT NULL,
  status TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  provider_channel TEXT NOT NULL,
  provider_request_id TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  failure_reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (merchant_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS provider_records (
  provider_channel TEXT NOT NULL,
  provider_request_id TEXT NOT NULL,
  order_id TEXT NOT NULL,
  amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  submit_calls INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  PRIMARY KEY (provider_channel, provider_request_id)
);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := migrateRefundIdempotency(db); err != nil {
		return fmt.Errorf("migrate refund records: %w", err)
	}
	if err := migratePaymentChannelStatus(db); err != nil {
		return fmt.Errorf("migrate payment channel status: %w", err)
	}
	if err := migrateDemoUnknownPayment(db); err != nil {
		return fmt.Errorf("migrate demo payment state: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_merchant ON orders(merchant_id, id);
CREATE INDEX IF NOT EXISTS idx_refunds_merchant ON refunds(merchant_id, created_at DESC);`); err != nil {
		return fmt.Errorf("create database indexes: %w", err)
	}
	return nil
}

func migratePaymentChannelStatus(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(orders)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primary int
		var name, dataType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		if name == "payment_channel_status" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE orders ADD COLUMN payment_channel_status TEXT NOT NULL DEFAULT 'UNKNOWN'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE orders SET payment_channel_status = CASE WHEN status = 'PAID' THEN 'SUCCEEDED' WHEN status = 'UNPAID' THEN 'UNPAID' ELSE 'UNKNOWN' END`); err != nil {
		return err
	}
	return tx.Commit()
}

// Existing demo databases need this correction once, without undoing a later sync.
func migrateDemoUnknownPayment(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	var applied int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = 'a1002_unknown_payment'`).Scan(&applied); err != nil {
		return err
	}
	if applied == 0 {
		if _, err = tx.Exec(`UPDATE orders SET status = 'UNKNOWN', payment_channel_status = 'UNKNOWN' WHERE id = 'A-1002' AND merchant_id = 'merchant-a' AND amount_cents = 8800 AND provider_channel = 'mock-unknown' AND status = 'PAID' AND NOT EXISTS (SELECT 1 FROM refunds WHERE order_id = 'A-1002')`); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations (name) VALUES ('a1002_unknown_payment')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrateRefundIdempotency(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(refunds)`)
	if err != nil {
		return err
	}
	hasReason := false
	for rows.Next() {
		var cid, notNull, primary int
		var name, dataType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		if name == "reason" {
			hasReason = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	indexes, err := db.Query(`PRAGMA index_list(refunds)`)
	if err != nil {
		return err
	}
	var indexNames []string
	for indexes.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := indexes.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			indexes.Close()
			return err
		}
		if unique == 1 {
			indexNames = append(indexNames, name)
		}
	}
	if err := indexes.Err(); err != nil {
		indexes.Close()
		return err
	}
	if err := indexes.Close(); err != nil {
		return err
	}
	globalKeyUnique := false
	for _, name := range indexNames {
		columns, err := db.Query(`PRAGMA index_info("` + name + `")`)
		if err != nil {
			return err
		}
		var found []string
		for columns.Next() {
			var seq, cid int
			var column string
			if err := columns.Scan(&seq, &cid, &column); err != nil {
				columns.Close()
				return err
			}
			found = append(found, column)
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			return err
		}
		if err := columns.Close(); err != nil {
			return err
		}
		if len(found) == 1 && found[0] == "idempotency_key" {
			globalKeyUnique = true
		}
	}
	if hasReason && !globalKeyUnique {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const replacement = `CREATE TABLE refunds_migration_new (
  id TEXT PRIMARY KEY,
  order_id TEXT NOT NULL UNIQUE REFERENCES orders(id),
  merchant_id TEXT NOT NULL,
  amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
  currency TEXT NOT NULL,
  status TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  provider_channel TEXT NOT NULL,
  provider_request_id TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  failure_reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (merchant_id, idempotency_key)
)`
	if _, err := tx.Exec(replacement); err != nil {
		return err
	}
	reasonExpr := `''`
	if hasReason {
		reasonExpr = `reason`
	}
	if _, err := tx.Exec(`INSERT INTO refunds_migration_new (id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, reason, failure_reason, created_at, updated_at)
SELECT id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, ` + reasonExpr + `, failure_reason, created_at, updated_at FROM refunds`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE refunds`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE refunds_migration_new RENAME TO refunds`); err != nil {
		return err
	}
	return tx.Commit()
}

func SeedDemoOrders(db *sql.DB) error {
	orders := []Order{
		{ID: "A-1001", MerchantID: "merchant-a", AmountCents: 19900, Currency: "CNY", Status: "PAID", ProviderChannel: "mock-success", PaymentChannelStatus: "SUCCEEDED"},
		{ID: "A-1002", MerchantID: "merchant-a", AmountCents: 8800, Currency: "CNY", Status: "UNKNOWN", ProviderChannel: "mock-unknown", PaymentChannelStatus: "UNKNOWN"},
		{ID: "A-1003", MerchantID: "merchant-a", AmountCents: 5600, Currency: "CNY", Status: "PAID", ProviderChannel: "mock-fail", PaymentChannelStatus: "SUCCEEDED"},
		{ID: "A-1004", MerchantID: "merchant-a", AmountCents: 12900, Currency: "CNY", Status: "UNPAID", ProviderChannel: "mock-success", PaymentChannelStatus: "UNPAID"},
		{ID: "B-2001", MerchantID: "merchant-b", AmountCents: 25600, Currency: "CNY", Status: "PAID", ProviderChannel: "mock-success", PaymentChannelStatus: "SUCCEEDED"},
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, order := range orders {
		// Correct only the original demo rows; keep amounts, refund IDs, and statuses intact.
		if _, err := tx.Exec(`UPDATE refunds SET currency = 'CNY' WHERE order_id = ? AND merchant_id = ? AND amount_cents = ? AND currency = 'USD' AND EXISTS (SELECT 1 FROM orders WHERE id = ? AND merchant_id = ? AND amount_cents = ? AND provider_channel = ? AND currency IN ('USD', 'CNY'))`, order.ID, order.MerchantID, order.AmountCents, order.ID, order.MerchantID, order.AmountCents, order.ProviderChannel); err != nil {
			return fmt.Errorf("update demo refund currency %s: %w", order.ID, err)
		}
		if _, err := tx.Exec(`UPDATE orders SET currency = 'CNY' WHERE id = ? AND merchant_id = ? AND amount_cents = ? AND provider_channel = ? AND currency = 'USD' AND NOT EXISTS (SELECT 1 FROM refunds WHERE order_id = ? AND (currency <> 'CNY' OR amount_cents <> ? OR merchant_id <> ?))`, order.ID, order.MerchantID, order.AmountCents, order.ProviderChannel, order.ID, order.AmountCents, order.MerchantID); err != nil {
			return fmt.Errorf("update demo order currency %s: %w", order.ID, err)
		}
		_, err := tx.Exec(`INSERT OR IGNORE INTO orders (id, merchant_id, amount_cents, currency, status, provider_channel, payment_channel_status) VALUES (?, ?, ?, ?, ?, ?, ?)`, order.ID, order.MerchantID, order.AmountCents, order.Currency, order.Status, order.ProviderChannel, order.PaymentChannelStatus)
		if err != nil {
			return fmt.Errorf("seed order %s: %w", order.ID, err)
		}
	}
	return tx.Commit()
}

// ResetDemoData is intentionally separate from normal initialization.
func ResetDemoData(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"refunds", "provider_records", "orders"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("reset %s: %w", table, err)
		}
	}
	return tx.Commit()
}
