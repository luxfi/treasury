package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const migrationSQL = `
CREATE TABLE IF NOT EXISTS ledger_accounts (
    id         BIGSERIAL PRIMARY KEY,
    ledger     VARCHAR NOT NULL,
    address    VARCHAR NOT NULL,
    type       VARCHAR NOT NULL DEFAULT 'unknown',
    metadata   JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(ledger, address)
);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id          BIGSERIAL PRIMARY KEY,
    ledger      VARCHAR NOT NULL,
    reference   VARCHAR,
    metadata    JSONB NOT NULL DEFAULT '{}',
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reverted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_reference ON ledger_transactions(ledger, reference) WHERE reference IS NOT NULL AND reference != '';

CREATE TABLE IF NOT EXISTS ledger_postings (
    id             BIGSERIAL PRIMARY KEY,
    ledger         VARCHAR NOT NULL,
    transaction_id BIGINT NOT NULL REFERENCES ledger_transactions(id),
    source         VARCHAR NOT NULL,
    destination    VARCHAR NOT NULL,
    asset          VARCHAR NOT NULL,
    amount         NUMERIC NOT NULL CHECK (amount > 0)
);
CREATE INDEX IF NOT EXISTS idx_postings_tx ON ledger_postings(transaction_id);

CREATE TABLE IF NOT EXISTS ledger_moves (
    id                  BIGSERIAL PRIMARY KEY,
    ledger              VARCHAR NOT NULL,
    transaction_id      BIGINT NOT NULL REFERENCES ledger_transactions(id),
    posting_id          BIGINT NOT NULL REFERENCES ledger_postings(id),
    account_address     VARCHAR NOT NULL,
    asset               VARCHAR NOT NULL,
    amount              NUMERIC NOT NULL,
    is_source           BOOLEAN NOT NULL,
    post_commit_inputs  NUMERIC NOT NULL DEFAULT 0,
    post_commit_outputs NUMERIC NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_moves_account_asset ON ledger_moves(ledger, account_address, asset);
CREATE INDEX IF NOT EXISTS idx_moves_tx ON ledger_moves(transaction_id);

CREATE TABLE IF NOT EXISTS ledger_logs (
    id              BIGSERIAL PRIMARY KEY,
    ledger          VARCHAR NOT NULL,
    type            VARCHAR NOT NULL,
    data            JSONB NOT NULL,
    idempotency_key VARCHAR,
    hash            BYTEA,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_logs_ledger ON ledger_logs(ledger, created_at DESC);
`

// PgStore implements Store using PostgreSQL.
type PgStore struct {
	db *sql.DB
}

func NewPgStore(databaseURL string) (*PgStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &PgStore{db: db}, nil
}

func (s *PgStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, migrationSQL)
	return err
}

func (s *PgStore) Close() error {
	return s.db.Close()
}

func (s *PgStore) CreateAccount(ctx context.Context, ledger string, acct *Account) error {
	meta, _ := json.Marshal(acct.Metadata)
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO ledger_accounts (ledger, address, type, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (ledger, address) DO NOTHING
		 RETURNING id`,
		ledger, acct.Address, acct.Type, meta, acct.CreatedAt, acct.UpdatedAt,
	).Scan(&acct.ID)
	if err == sql.ErrNoRows {
		// Account already exists — fetch it
		return s.db.QueryRowContext(ctx,
			`SELECT id FROM ledger_accounts WHERE ledger = $1 AND address = $2`,
			ledger, acct.Address,
		).Scan(&acct.ID)
	}
	return err
}

func (s *PgStore) GetAccount(ctx context.Context, ledger, address string) (*Account, error) {
	acct := &Account{}
	var meta []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id, ledger, address, type, metadata, created_at, updated_at
		 FROM ledger_accounts WHERE ledger = $1 AND address = $2`,
		ledger, address,
	).Scan(&acct.ID, &acct.Ledger, &acct.Address, &acct.Type, &meta, &acct.CreatedAt, &acct.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("account %q not found in ledger %q", address, ledger)
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(meta, &acct.Metadata)
	return acct, nil
}

func (s *PgStore) ListAccounts(ctx context.Context, ledger string) ([]*Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ledger, address, type, metadata, created_at, updated_at
		 FROM ledger_accounts WHERE ledger = $1 ORDER BY id`,
		ledger,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Account
	for rows.Next() {
		acct := &Account{}
		var meta []byte
		if err := rows.Scan(&acct.ID, &acct.Ledger, &acct.Address, &acct.Type, &meta, &acct.CreatedAt, &acct.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &acct.Metadata)
		result = append(result, acct)
	}
	return result, rows.Err()
}

func (s *PgStore) UpdateAccountMetadata(ctx context.Context, ledger, address string, metadata map[string]string) error {
	meta, _ := json.Marshal(metadata)
	_, err := s.db.ExecContext(ctx,
		`UPDATE ledger_accounts SET metadata = metadata || $3, updated_at = NOW()
		 WHERE ledger = $1 AND address = $2`,
		ledger, address, meta,
	)
	return err
}

func (s *PgStore) InsertTransaction(ctx context.Context, ledger string, tx *Transaction, moves []Move) error {
	dbTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbTx.Rollback()

	meta, _ := json.Marshal(tx.Metadata)
	var ref *string
	if tx.Reference != "" {
		ref = &tx.Reference
	}
	err = dbTx.QueryRowContext(ctx,
		`INSERT INTO ledger_transactions (ledger, reference, metadata, timestamp)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		ledger, ref, meta, tx.Timestamp,
	).Scan(&tx.ID)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}

	// Insert postings and collect IDs
	moveIdx := 0
	for i, p := range tx.Postings {
		var postingID int64
		err = dbTx.QueryRowContext(ctx,
			`INSERT INTO ledger_postings (ledger, transaction_id, source, destination, asset, amount)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			ledger, tx.ID, p.Source, p.Destination, p.Asset, p.Amount,
		).Scan(&postingID)
		if err != nil {
			return fmt.Errorf("insert posting[%d]: %w", i, err)
		}
		tx.Postings[i].ID = postingID
		tx.Postings[i].TransactionID = tx.ID

		// Two moves per posting: source (debit) + destination (credit)
		for j := 0; j < 2 && moveIdx < len(moves); j++ {
			m := &moves[moveIdx]
			m.TransactionID = tx.ID
			m.PostingID = postingID
			_, err = dbTx.ExecContext(ctx,
				`INSERT INTO ledger_moves (ledger, transaction_id, posting_id, account_address, asset, amount, is_source, post_commit_inputs, post_commit_outputs, created_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				ledger, m.TransactionID, m.PostingID, m.AccountAddress, m.Asset, m.Amount,
				m.IsSource, m.PostCommitInputs, m.PostCommitOutputs, m.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("insert move: %w", err)
			}
			moveIdx++
		}
	}

	return dbTx.Commit()
}

func (s *PgStore) GetTransaction(ctx context.Context, ledger string, id int64) (*Transaction, error) {
	tx := &Transaction{}
	var meta []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id, ledger, COALESCE(reference, ''), metadata, timestamp, reverted_at
		 FROM ledger_transactions WHERE ledger = $1 AND id = $2`,
		ledger, id,
	).Scan(&tx.ID, &tx.Ledger, &tx.Reference, &meta, &tx.Timestamp, &tx.RevertedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("transaction %d not found in ledger %q", id, ledger)
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(meta, &tx.Metadata)

	// Load postings
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, transaction_id, source, destination, asset, amount::text
		 FROM ledger_postings WHERE ledger = $1 AND transaction_id = $2 ORDER BY id`,
		ledger, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Posting
		if err := rows.Scan(&p.ID, &p.TransactionID, &p.Source, &p.Destination, &p.Asset, &p.Amount); err != nil {
			return nil, err
		}
		tx.Postings = append(tx.Postings, p)
	}
	return tx, rows.Err()
}

func (s *PgStore) GetTransactionByReference(ctx context.Context, ledger, reference string) (*Transaction, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE ledger = $1 AND reference = $2`,
		ledger, reference,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("transaction with reference %q not found", reference)
	}
	if err != nil {
		return nil, err
	}
	return s.GetTransaction(ctx, ledger, id)
}

func (s *PgStore) ListTransactions(ctx context.Context, ledger string, limit, offset int) ([]*Transaction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ledger, COALESCE(reference, ''), metadata, timestamp, reverted_at
		 FROM ledger_transactions WHERE ledger = $1
		 ORDER BY id DESC LIMIT $2 OFFSET $3`,
		ledger, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Transaction
	for rows.Next() {
		tx := &Transaction{}
		var meta []byte
		if err := rows.Scan(&tx.ID, &tx.Ledger, &tx.Reference, &meta, &tx.Timestamp, &tx.RevertedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &tx.Metadata)
		result = append(result, tx)
	}
	return result, rows.Err()
}

func (s *PgStore) RevertTransaction(ctx context.Context, ledger string, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ledger_transactions SET reverted_at = NOW()
		 WHERE ledger = $1 AND id = $2 AND reverted_at IS NULL`,
		ledger, id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("transaction %d not found or already reverted", id)
	}
	return nil
}

func (s *PgStore) GetAccountBalance(ctx context.Context, ledger, address, asset string) (string, string, error) {
	var inputs, outputs string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(post_commit_inputs::text, '0'), COALESCE(post_commit_outputs::text, '0')
		 FROM ledger_moves
		 WHERE ledger = $1 AND account_address = $2 AND asset = $3
		 ORDER BY id DESC LIMIT 1`,
		ledger, address, asset,
	).Scan(&inputs, &outputs)
	if err == sql.ErrNoRows {
		return "0", "0", nil
	}
	return inputs, outputs, err
}

func (s *PgStore) GetAccountBalances(ctx context.Context, ledger, address string) ([]AccountBalance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT ON (asset) asset, post_commit_inputs::text, post_commit_outputs::text
		 FROM ledger_moves
		 WHERE ledger = $1 AND account_address = $2
		 ORDER BY asset, id DESC`,
		ledger, address,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AccountBalance
	for rows.Next() {
		var asset, inputs, outputs string
		if err := rows.Scan(&asset, &inputs, &outputs); err != nil {
			return nil, err
		}
		in, _ := new(big.Int).SetString(inputs, 10)
		out, _ := new(big.Int).SetString(outputs, 10)
		if in == nil {
			in = new(big.Int)
		}
		if out == nil {
			out = new(big.Int)
		}
		bal := new(big.Int).Sub(in, out)
		result = append(result, AccountBalance{
			Account: address,
			Asset:   asset,
			Balance: bal.String(),
			Volumes: Volumes{Inputs: in.String(), Outputs: out.String()},
		})
	}
	return result, rows.Err()
}

func (s *PgStore) InsertLog(ctx context.Context, log *LogEntry) error {
	data, _ := json.Marshal(log.Data)
	return s.db.QueryRowContext(ctx,
		`INSERT INTO ledger_logs (ledger, type, data, idempotency_key, hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		log.Ledger, log.Type, data, log.IdempotencyKey, log.Hash, log.CreatedAt,
	).Scan(&log.ID)
}

func (s *PgStore) ListLogs(ctx context.Context, ledger string, limit, offset int) ([]*LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ledger, type, data, COALESCE(idempotency_key, ''), hash, created_at
		 FROM ledger_logs WHERE ledger = $1
		 ORDER BY id DESC LIMIT $2 OFFSET $3`,
		ledger, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*LogEntry
	for rows.Next() {
		l := &LogEntry{}
		var data []byte
		if err := rows.Scan(&l.ID, &l.Ledger, &l.Type, &data, &l.IdempotencyKey, &l.Hash, &l.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(data, &l.Data)
		result = append(result, l)
	}
	return result, rows.Err()
}
