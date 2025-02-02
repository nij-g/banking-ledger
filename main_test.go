// main_test.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type BankingTestSuite struct {
	suite.Suite
	server *Server
	router *gin.Engine
	ctx    context.Context
}

func TestBankingSuite(t *testing.T) {
	suite.Run(t, new(BankingTestSuite))
}

func (s *BankingTestSuite) SetupSuite() {
	s.ctx = context.Background()

	// Use Docker service URLs from environment variables or fall back to defaults
	postgresURL := os.Getenv("TEST_POSTGRES_URL")
	if postgresURL == "" {
		postgresURL = "postgresql://postgres:postgres@postgres:5432/banking"
	}
	
	// Add connection retry logic for PostgreSQL
	var dbpool *pgxpool.Pool
	var err error
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		dbpool, err = pgxpool.Connect(s.ctx, postgresURL)
		if err == nil {
			break
		}
		s.T().Logf("Attempt %d: Failed to connect to PostgreSQL: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		s.T().Fatalf("Failed to connect to PostgreSQL after %d attempts: %v", maxRetries, err)
	}

	// MongoDB connection with retry logic
	mongoURL := os.Getenv("TEST_MONGO_URL")
	if mongoURL == "" {
		mongoURL = "mongodb://mongo:27017"
	}

	var mongoClient *mongo.Client
	for i := 0; i < maxRetries; i++ {
		mongoClient, err = mongo.Connect(s.ctx, options.Client().ApplyURI(mongoURL))
		if err == nil {
			// Test the connection
			err = mongoClient.Ping(s.ctx, nil)
			if err == nil {
				break
			}
		}
		s.T().Logf("Attempt %d: Failed to connect to MongoDB: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		s.T().Fatalf("Failed to connect to MongoDB after %d attempts: %v", maxRetries, err)
	}

	// Initialize test schema in PostgreSQL
	_, err = dbpool.Exec(s.ctx, `
		CREATE TABLE IF NOT EXISTS accounts (
			id VARCHAR(255) PRIMARY KEY,
			balance DECIMAL(15,2) NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			version INTEGER DEFAULT 1
		)
	`)
	if err != nil {
		s.T().Fatalf("Failed to create test schema: %v", err)
	}

	s.server = &Server{
		db:    dbpool,
		mongo: mongoClient,
	}

	gin.SetMode(gin.TestMode)
	s.router = gin.New()
	s.server.setupRoutes(s.router)
}

func (s *BankingTestSuite) TearDownSuite() {
	if s.server.db != nil {
		s.server.db.Close()
	}
	if s.server.mongo != nil {
		s.server.mongo.Disconnect(s.ctx)
	}
}

func (s *BankingTestSuite) SetupTest() {
	// Clean up test data before each test
	if s.server.db != nil {
		_, err := s.server.db.Exec(s.ctx, "DELETE FROM accounts")
		if err != nil {
			s.T().Logf("Failed to clean up accounts table: %v", err)
		}
	}

	if s.server.mongo != nil {
		err := s.server.mongo.Database("banking").Collection("ledger").Drop(s.ctx)
		if err != nil {
			s.T().Logf("Failed to clean up ledger collection: %v", err)
		}
	}
}
// Individual test methods

func (s *BankingTestSuite) TestDeposit() {
	s.T().Log("Starting TestDeposit")
	
	// Create test account
	account := s.createTestAccount(1000.00)
	s.T().Logf("Created test account with ID: %s and balance: %.2f", account.ID, account.Balance)
	
	// Test case 1: Valid deposit
	depositReq := TransactionRequest{
		Amount:         500.00,
		Description:    "Test deposit",
		IdempotencyKey: "test-deposit-1",
	}
	
	jsonData, err := json.Marshal(depositReq)
	s.Require().NoError(err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", fmt.Sprintf("/accounts/%s/deposit", account.ID), bytes.NewBuffer(jsonData))
	s.router.ServeHTTP(w, req)

	s.T().Logf("Deposit response status: %d", w.Code)
	s.Equal(200, w.Code)
	
	var ledgerEntry LedgerEntry
	err = json.Unmarshal(w.Body.Bytes(), &ledgerEntry)
	s.Require().NoError(err)
	s.Equal(account.ID, ledgerEntry.AccountID)
	s.Equal(500.00, ledgerEntry.Amount)
	s.Equal(1500.00, ledgerEntry.NewBalance)
	s.T().Logf("Deposit successful. New balance: %.2f", ledgerEntry.NewBalance)
}

func (s *BankingTestSuite) TestWithdrawal() {
	s.T().Log("Starting TestWithdrwawal")
	
	// Create test account
	account := s.createTestAccount(1000.00)
	s.T().Logf("Created test account with ID: %s and balance: %.2f", account.ID, account.Balance)
	
	// Test case 1: Valid withdrawal
	withdrawalReq := TransactionRequest{
		Amount:         500.00,
		Description:    "Test withdrawal",
		IdempotencyKey: "test-withdrawal-1",
	}
	
	jsonData, err := json.Marshal(withdrawalReq)
	s.Require().NoError(err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", fmt.Sprintf("/accounts/%s/withdraw", account.ID), bytes.NewBuffer(jsonData))
	s.router.ServeHTTP(w, req)

	s.T().Logf("withdrawal response status: %d", w.Code)
	s.Equal(200, w.Code)
	
	var ledgerEntry LedgerEntry
	err = json.Unmarshal(w.Body.Bytes(), &ledgerEntry)
	s.Require().NoError(err)
	s.Equal(account.ID, ledgerEntry.AccountID)
	s.Equal(500.00, ledgerEntry.Amount)
	s.Equal(500.00, ledgerEntry.NewBalance)
	s.T().Logf("withdrawal successful. New balance: %.2f", ledgerEntry.NewBalance)
}
// Helper methods

func (s *BankingTestSuite) createTestAccount(balance float64) Account {
	s.T().Logf("Creating test account with initial balance: %.2f", balance)
	
	account := Account{Balance: balance}
	jsonData, err := json.Marshal(account)
	s.Require().NoError(err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/accounts", bytes.NewBuffer(jsonData))
	s.router.ServeHTTP(w, req)

	var response Account
	err = json.Unmarshal(w.Body.Bytes(), &response)
	s.Require().NoError(err)
	
	s.T().Logf("Test account created with ID: %s", response.ID)
	return response
}