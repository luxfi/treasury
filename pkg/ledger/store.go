package ledger

import "context"

// Store is the persistence interface for the ledger.
// Implementations: PgStore (Postgres), MemStore (in-memory for tests).
type Store interface {
	// Accounts
	CreateAccount(ctx context.Context, ledger string, acct *Account) error
	GetAccount(ctx context.Context, ledger, address string) (*Account, error)
	ListAccounts(ctx context.Context, ledger string) ([]*Account, error)
	UpdateAccountMetadata(ctx context.Context, ledger, address string, metadata map[string]string) error

	// Transactions (atomic: creates transaction + postings + moves)
	InsertTransaction(ctx context.Context, ledger string, tx *Transaction, moves []Move) error
	GetTransaction(ctx context.Context, ledger string, id int64) (*Transaction, error)
	GetTransactionByReference(ctx context.Context, ledger, reference string) (*Transaction, error)
	ListTransactions(ctx context.Context, ledger string, limit, offset int) ([]*Transaction, error)
	RevertTransaction(ctx context.Context, ledger string, id int64) error

	// Balances (computed from moves)
	GetAccountBalance(ctx context.Context, ledger, address, asset string) (inputs, outputs string, err error)
	GetAccountBalances(ctx context.Context, ledger, address string) ([]AccountBalance, error)

	// Logs
	InsertLog(ctx context.Context, log *LogEntry) error
	ListLogs(ctx context.Context, ledger string, limit, offset int) ([]*LogEntry, error)

	// Lifecycle
	Migrate(ctx context.Context) error
	Close() error
}
