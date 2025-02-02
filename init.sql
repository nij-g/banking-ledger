-- init.sql
CREATE TABLE IF NOT EXISTS accounts (
    id VARCHAR(255) PRIMARY KEY,
    balance DECIMAL(15,2) NOT NULL CHECK (balance >= 0),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_accounts_balance ON accounts(balance);

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_accounts_updated_at
    BEFORE UPDATE ON accounts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add transaction amount constraints
ALTER TABLE accounts ADD CONSTRAINT positive_balance CHECK (balance >= 0);
ALTER TABLE accounts ADD COLUMN version INTEGER DEFAULT 0;
CREATE UNIQUE INDEX idx_account_version ON accounts(id, version);
CREATE UNIQUE INDEX idx_ledger_idempotency ON ledger(idempotency_key);