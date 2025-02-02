// main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"
    "crypto/rand"
    "encoding/hex"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/streadway/amqp"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Account struct {
    ID      string  `json:"id"`
    Balance float64 `json:"balance"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

type Transaction struct {
    ID          string  `json:"id"`
    AccountID   string  `json:"account_id"`
    Amount      float64 `json:"amount"`
    Type        string    `json:"type"` // "deposit" or "withdrawal"
    Status      string    `json:"status"` // "pending", "completed", "failed"
    Timestamp   time.Time `json:"timestamp"`
    
}

type LedgerEntry struct {
    ID              string    `json:"id" bson:"_id"`
    AccountID       string    `json:"account_id" bson:"account_id"`
    TransactionID   string    `json:"transaction_id" bson:"transaction_id"`
    Type           string    `json:"type" bson:"type"`
    Amount         float64   `json:"amount" bson:"amount"`
    PreviousBalance float64   `json:"previous_balance" bson:"previous_balance"`
    NewBalance     float64   `json:"new_balance" bson:"new_balance"`
    Description    string    `json:"description" bson:"description"`
    Metadata       map[string]interface{} `json:"metadata" bson:"metadata"`
    CreatedAt      time.Time `json:"created_at" bson:"created_at"`
    Status         string    `json:"status" bson:"status"`
    IdempotencyKey  string `json:"idempotency_key" binding:"required"`
}

type TransactionRequest struct {
    IdempotencyKey string            `json:"idempotencyKey"`
    Amount         float64           `json:"amount"`
    Description    string            `json:"description"`
    Metadata       map[string]string `json:"metadata"`
    FromAccountID  string            `json:"fromAccountID"` 
    ToAccountID    string            `json:"toAccountID"`   
}

type LedgerQuery struct {
    StartDate   *time.Time `form:"start_date"`
    EndDate     *time.Time `form:"end_date"`
    Type        string    `form:"type"`
    MinAmount   *float64  `form:"min_amount"`
    MaxAmount   *float64  `form:"max_amount"`
    Limit       int       `form:"limit,default=50"`
    Offset      int       `form:"offset,default=0"`
}

type Server struct {
    db          *pgxpool.Pool
    mongo       *mongo.Client
    rabbitmq    *amqp.Connection
    rabbitmqCh  *amqp.Channel
}

func main() {
    server := &Server{}
    
    // Initialize PostgreSQL
    postgresURL := os.Getenv("POSTGRES_URL")
    dbpool, err := pgxpool.Connect(context.Background(), postgresURL)
    if err != nil {
        log.Fatalf("Unable to connect to PostgreSQL: %v", err)
    }
    defer dbpool.Close()
    server.db = dbpool

    // Initialize MongoDB
    mongoURL := os.Getenv("MONGO_URL")
    mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURL))
    if err != nil {
        log.Fatalf("Unable to connect to MongoDB: %v", err)
    }
    defer mongoClient.Disconnect(context.Background())
    server.mongo = mongoClient

    // Initialize RabbitMQ
    rabbitmqURL := os.Getenv("RABBITMQ_URL")
    conn, err := amqp.Dial(rabbitmqURL)
    if err != nil {
        log.Fatalf("Unable to connect to RabbitMQ: %v", err)
    }
    defer conn.Close()
    server.rabbitmq = conn

    ch, err := conn.Channel()
    if err != nil {
        log.Fatalf("Unable to open RabbitMQ channel: %v", err)
    }
    defer ch.Close()
    server.rabbitmqCh = ch

    // Declare RabbitMQ queue
    q, err := ch.QueueDeclare(
        "transactions",
        true,  // durable
        false, // delete when unused
        false, // exclusive
        false, // no-wait
        nil,   // arguments
    )
    if err != nil {
        log.Fatalf("Failed to declare queue: %v", err)
    }
    fmt.Printf("Queue declared: %s\n", q.Name)
    // Initialize Gin router
    r := gin.Default()
    
    server.setupRoutes(r)
    r.Run(":8080")
}
func (s *Server) setupRoutes(r *gin.Engine) {
    r.POST("/accounts", s.createAccount)
    r.GET("/accounts/:id", s.getAccount)
    r.POST("/accounts/:id/deposit", s.deposit)
    r.POST("/accounts/:id/withdraw", s.withdraw)
    r.GET("/accounts/:id/transactions", s.getTransactions)
    r.GET("/accounts/:id/ledger", s.getLedger)
    r.GET("/accounts/:id/balance-history", s.getBalanceHistory)
    r.GET("/accounts/:id/statement", s.generateStatement)
    r.POST("/accounts/transfer/", s.transfer)
}

func (s *Server) createAccount(c *gin.Context) {
    var account Account
    if err := c.ShouldBindJSON(&account); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    // Generate a unique ID if not provided
    if account.ID == "" {
        account.ID = GenerateUUID()
    }

    // Set creation timestamps
    now := time.Now()
    account.CreatedAt = now
    account.UpdatedAt = now

    // Upsert operation to handle potential duplicates
    _, err := s.db.Exec(context.Background(),
        `INSERT INTO accounts (id, balance, created_at, updated_at) 
         VALUES ($1, $2, $3, $4)
         ON CONFLICT (id) DO UPDATE 
         SET balance = EXCLUDED.balance, 
             updated_at = EXCLUDED.updated_at`,
        account.ID, account.Balance, account.CreatedAt, account.UpdatedAt)
    
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }

    c.JSON(201, account)
}

func (s *Server) getAccount(c *gin.Context) {
    id := c.Param("id")
    
    var account Account
    err := s.db.QueryRow(context.Background(),
        "SELECT id, balance FROM accounts WHERE id = $1", id).
        Scan(&account.ID, &account.Balance)
    if err != nil {
        c.JSON(404, gin.H{"error": "Account not found"})
        return
    }

    c.JSON(200, account)
}


func (s *Server) getTransactions(c *gin.Context) {
    accountID := c.Param("id")
    
    collection := s.mongo.Database("banking").Collection("transactions")
    cursor, err := collection.Find(context.Background(), map[string]string{"account_id": accountID})
    if err != nil {
        c.JSON(500, gin.H{"error": "Failed to fetch transactions"})
        return
    }
    
    var transactions []Transaction
    if err = cursor.All(context.Background(), &transactions); err != nil {
        c.JSON(500, gin.H{"error": "Failed to decode transactions"})
        return
    }

    c.JSON(200, transactions)
}

func (s *Server) deposit(c *gin.Context) {
    accountID := c.Param("id")
    var req TransactionRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": "Invalid request body"})
        return
    }

    tx, err := s.processTransaction(c.Request.Context(), accountID, "", req.Amount, "deposit", req)
    if err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    c.JSON(200, tx)
}
//withdraw amoount
func (s *Server) withdraw(c *gin.Context) {
    accountID := c.Param("id")
    var req TransactionRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": "Invalid request body", "details": err.Error()})
        return
    }

    tx, err := s.processTransaction(c.Request.Context(), accountID, "", req.Amount, "withdrawal", req)
    if err != nil {
        c.JSON(400, gin.H{"error here": err.Error()})
        return
    }

    c.JSON(200, tx)
}

var (
    accountLocks = make(map[string]*sync.RWMutex)
    accountLocksMutex sync.Mutex
)

func getAccountLock(accountID string) *sync.RWMutex {
    accountLocksMutex.Lock()
    defer accountLocksMutex.Unlock()
    
    if lock, exists := accountLocks[accountID]; exists {
        return lock
    }
    
    newLock := &sync.RWMutex{}
    accountLocks[accountID] = newLock
    return newLock
}

// processTransaction - Handles both deposit and transfer types
func (s *Server) processTransaction(ctx context.Context, fromAccountID, toAccountID string, amount float64, txType string, req TransactionRequest) (*LedgerEntry, error) {
    var accountLock1, accountLock2 *sync.RWMutex

    // Lock accounts based on transaction type
    accountLock1 = getAccountLock(fromAccountID) // Lock the "from" account
    if txType == "transfer" {
        accountLock2 = getAccountLock(toAccountID) // Lock the "to" account for transfer
    }

    // Lock the "from" account (and "to" account for transfers)
    accountLock1.Lock()
    defer accountLock1.Unlock()
    if txType == "transfer" {
        accountLock2.Lock()
        defer accountLock2.Unlock()
    }

    // Start a serializable transaction for strongest isolation
    tx, err := s.db.Begin(ctx)
    if err != nil {
        return nil, fmt.Errorf("transaction start failed: %v", err)
    }
    defer tx.Rollback(ctx)

    tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")

    // Variables for account details and new balances
    var fromAccount, toAccount Account
    var newFromBalance, newToBalance float64
    metadata := make(map[string]interface{})
    for key, value := range req.Metadata {
        metadata[key] = value
    }

    // Handle deposit (only one account)
    if txType == "deposit" || txType == "withdrawal" {
        // Fetch account balance for deposit operation
        err = tx.QueryRow(ctx, "SELECT id, balance FROM accounts WHERE id = $1 FOR UPDATE", fromAccountID).Scan(&fromAccount.ID, &fromAccount.Balance)
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return nil, errors.New("account not found")
            }
            return nil, fmt.Errorf("account fetch failed: %v", err)
        }

        // Update balance for deposit
        if txType == "withdrawal" {
            newFromBalance = fromAccount.Balance - amount
        }else { 
            newFromBalance = fromAccount.Balance + amount
        }
        // Create ledger entry for deposit
        ledgerEntry := &LedgerEntry{
            ID:              GenerateUUID(),
            AccountID:       fromAccountID,
            TransactionID:   GenerateUUID(),
            IdempotencyKey:  req.IdempotencyKey,
            Type:            txType, // or "credit"
            Amount:          amount,
            PreviousBalance: fromAccount.Balance,
            NewBalance:      newFromBalance,
            Description:     req.Description,
            Metadata:        metadata, // Assuming req.Metadata is map[string]interface{}
            CreatedAt:       time.Now(),
            Status:          "completed",
        }

        // Update account balance with version control
        _, err = tx.Exec(ctx, `UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2 AND balance = $3`,
            newFromBalance, fromAccountID, fromAccount.Balance)
        if err != nil {
            return nil, fmt.Errorf("balance update failed: %v", err)
        }

        // Store ledger entry in MongoDB
        collection := s.mongo.Database("banking").Collection("ledger")
        _, err = collection.InsertOne(ctx, ledgerEntry)
        if err != nil {
            return nil, fmt.Errorf("ledger insertion failed: %v", err)
        }

        // Commit transaction
        if err = tx.Commit(ctx); err != nil {
            return nil, fmt.Errorf("transaction commit failed: %v", err)
        }

        // Publish ledger event asynchronously
        go s.publishLedgerEvent(ledgerEntry)

        return ledgerEntry, nil
    }

    // Handle transfer (two accounts involved)
    if txType == "transfer" {
        // Fetch account balances for both accounts involved in transfer
        err = tx.QueryRow(ctx, "SELECT id, balance FROM accounts WHERE id = $1 FOR UPDATE", fromAccountID).Scan(&fromAccount.ID, &fromAccount.Balance)
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return nil, errors.New("from account not found")
            }
            return nil, fmt.Errorf("from account fetch failed: %v", err)
        }

        err = tx.QueryRow(ctx, "SELECT id, balance FROM accounts WHERE id = $1 FOR UPDATE", toAccountID).Scan(&toAccount.ID, &toAccount.Balance)
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return nil, errors.New("to account not found")
            }
            return nil, fmt.Errorf("to account fetch failed: %v", err)
        }

        // Validate transfer: ensure sufficient funds in the source account
        newFromBalance = fromAccount.Balance - amount
        if newFromBalance < 0 {
            return nil, errors.New("insufficient funds in source account")
        }
        newToBalance = toAccount.Balance + amount

        // Create ledger entries for both accounts involved in the transfer
        ledgerEntryFrom := &LedgerEntry{
            ID:              GenerateUUID(),
            AccountID:       fromAccountID,
            TransactionID:   GenerateUUID(),
            IdempotencyKey:  req.IdempotencyKey,
            Type:            "withdrawal", 
            Amount:          amount,
            PreviousBalance: fromAccount.Balance,
            NewBalance:      newFromBalance,
            Description:     req.Description,
            Metadata:        metadata,
            CreatedAt:       time.Now(),
            Status:          "completed",
        }

        ledgerEntryTo := &LedgerEntry{
            ID:              GenerateUUID(),
            AccountID:       toAccountID,
            TransactionID:   ledgerEntryFrom.TransactionID, // Same transaction ID for both accounts
            IdempotencyKey:  req.IdempotencyKey,
            Type:            "deposit", 
            Amount:          amount,
            PreviousBalance: toAccount.Balance,
            NewBalance:      newToBalance,
            Description:     req.Description,
            Metadata:        metadata,
            CreatedAt:       time.Now(),
            Status:          "completed",
        }

        // Update both accounts' balances with version control
        _, err = tx.Exec(ctx, `UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2 AND balance = $3`,
            newFromBalance, fromAccountID, fromAccount.Balance)
        if err != nil {
            return nil, fmt.Errorf("source account balance update failed: %v", err)
        }

        _, err = tx.Exec(ctx, `UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2 AND balance = $3`,
            newToBalance, toAccountID, toAccount.Balance)
        if err != nil {
            return nil, fmt.Errorf("destination account balance update failed: %v", err)
        }

        // Store ledger entries in MongoDB
        collection := s.mongo.Database("banking").Collection("ledger")
        _, err = collection.InsertOne(ctx, ledgerEntryFrom)
        if err != nil {
            return nil, fmt.Errorf("ledger insertion failed for from account: %v", err)
        }

        _, err = collection.InsertOne(ctx, ledgerEntryTo)
        if err != nil {
            return nil, fmt.Errorf("ledger insertion failed for to account: %v", err)
        }

        // Commit transaction
        if err = tx.Commit(ctx); err != nil {
            return nil, fmt.Errorf("transaction commit failed: %v", err)
        }

        // Publish ledger event asynchronously
        go s.publishLedgerEvent(ledgerEntryFrom)
        go s.publishLedgerEvent(ledgerEntryTo)

        return ledgerEntryFrom, nil
    }

    return nil, errors.New("invalid transaction type")
}


func (s *Server) getLedger(c *gin.Context) {
    accountID := c.Param("id")
    var query LedgerQuery
    if err := c.ShouldBindQuery(&query); err != nil {
        c.JSON(400, gin.H{"error": "Invalid query parameters"})
        return
    }

    // Build MongoDB query
    filter := bson.M{"account_id": accountID}
    if query.StartDate != nil {
        filter["created_at"] = bson.M{"$gte": query.StartDate}
    }
    if query.EndDate != nil {
        if filter["created_at"] == nil {
            filter["created_at"] = bson.M{}
        }
        filter["created_at"].(bson.M)["$lte"] = query.EndDate
    }
    if query.Type != "" {
        filter["type"] = query.Type
    }
    if query.MinAmount != nil {
        filter["amount"] = bson.M{"$gte": query.MinAmount}
    }
    if query.MaxAmount != nil {
        if filter["amount"] == nil {
            filter["amount"] = bson.M{}
        }
        filter["amount"].(bson.M)["$lte"] = query.MaxAmount
    }

    // Execute query
    collection := s.mongo.Database("banking").Collection("ledger")
    opts := options.Find().
        SetSort(bson.D{{Key: "created_at", Value: -1}}).
        SetLimit(int64(query.Limit)).
        SetSkip(int64(query.Offset))

    cursor, err := collection.Find(c.Request.Context(), filter, opts)
    if err != nil {
        c.JSON(500, gin.H{"error": "Failed to fetch ledger entries"})
        return
    }
    defer cursor.Close(c.Request.Context())

    var entries []LedgerEntry
    if err = cursor.All(c.Request.Context(), &entries); err != nil {
        c.JSON(500, gin.H{"error": "Failed to decode ledger entries"})
        return
    }

    c.JSON(200, entries)
}

func (s *Server) getBalanceHistory(c *gin.Context) {
    accountID := c.Param("id")
    collection := s.mongo.Database("banking").Collection("ledger")
    
    pipeline := []bson.M{
        {
            "$match": bson.M{
                "account_id": accountID, // Ensure accountID is passed correctly as a variable
            },
        },
        {
            "$group": bson.M{
                "_id": bson.M{
                    "$dateToString": bson.M{
                        "format": "%Y-%m-%d", // Format to group by date
                        "date": "$created_at", // Ensure created_at is a valid date
                    },
                },
                "end_balance": bson.M{"$last": "$new_balance"},
                "transactions": bson.M{"$sum": 1},
                "total_credits": bson.M{
                    "$sum": bson.M{
                        "$cond": bson.A{ // Use bson.A for arrays in $cond
                            bson.M{"$gt": bson.A{"$amount", 0}}, // Condition
                            "$amount", // True case
                            0,         // False case
                        },
                    },
                },
                "total_debits": bson.M{
                    "$sum": bson.M{
                        "$cond": bson.A{
                            bson.M{"$lt": bson.A{"$amount", 0}},
                            bson.M{"$abs": "$amount"},
                            0,
                        },
                    },
                },
            },
        },
        {
            "$sort": bson.M{"_id": 1}, // Sort by date (_id contains the formatted date string)
        },
    }

    cursor, err := collection.Aggregate(c.Request.Context(), pipeline)
    if err != nil {
        c.JSON(500, gin.H{"error": "Failed to fetch balance history"})
        return
    }
    defer cursor.Close(c.Request.Context())

    var history []bson.M
    if err = cursor.All(c.Request.Context(), &history); err != nil {
        c.JSON(500, gin.H{"error": "Failed to decode balance history"})
        return
    }

    c.JSON(200, history)
}


func (s *Server) publishLedgerEvent(entry *LedgerEntry) {
    // Validate the entry before publishing
    if entry == nil {
        log.Println("Error: Attempted to publish nil ledger entry")
        return
    }

     // Ensure RabbitMQ connection exists
     if s.rabbitmq == nil || s.rabbitmqCh == nil {
        log.Println("Error: RabbitMQ connection not established")
        return
    }

    // Prepare the message payload
    payload, err := json.Marshal(entry)
    if err != nil {
        log.Printf("Failed to marshal ledger entry: %v\n", err)
        return
    }

    // Publish to RabbitMQ with error handling
    err = s.rabbitmqCh.Publish(
        "ledger_exchange",   // exchange name
        "ledger_routing_key", // routing key
        false,  // mandatory
        false,  // immediate
        amqp.Publishing{
            ContentType: "application/json",
            Body:        payload,
            Timestamp:   time.Now(),
            MessageId:   entry.ID,
            Headers: amqp.Table{
                "account_id":     entry.AccountID,
                "transaction_id": entry.TransactionID,
                "type":           entry.Type,
            },
        },
    )

    if err != nil {
        log.Printf("Failed to publish ledger event: %v\n", err)
        
        // Fallback logging or error handling
        fallbackLog := fmt.Sprintf("FALLBACK_LOG: AccountID=%s, TransactionID=%s, Type=%s, Amount=%f",
            entry.AccountID, entry.TransactionID, entry.Type, entry.Amount)
        log.Println(fallbackLog)
    }
}

func (s *Server) generateStatement(c *gin.Context) {
    accountID := c.Param("id")
    startDateStr := c.Query("start_date")
    endDateStr := c.Query("end_date")

    // Parse dates with error handling
    var startDate, endDate time.Time
    var err error

    if startDateStr == "" {
        // Default to 30 days ago if no start date provided
        startDate = time.Now().AddDate(0, 0, -30)
    } else {
        startDate, err = time.Parse(time.RFC3339, startDateStr)
        if err != nil {
            c.JSON(400, gin.H{"error": "Invalid start date format. Use RFC3339 format"})
            return
        }
    }

    if endDateStr == "" {
        // Default to current time if no end date provided
        endDate = time.Now()
    } else {
        endDate, err = time.Parse(time.RFC3339, endDateStr)
        if err != nil {
            c.JSON(400, gin.H{"error": "Invalid end date format. Use RFC3339 format"})
            return
        }
    }

    // Ensure start date is before end date
    if startDate.After(endDate) {
        c.JSON(400, gin.H{"error": "Start date must be before end date"})
        return
    }

    // Prepare statement structure
    var statement struct {
        AccountInfo     Account       `json:"account_info"`
        StartDate       string        `json:"start_date"`
        EndDate         string        `json:"end_date"`
        StartBalance    float64       `json:"start_balance"`
        EndBalance      float64       `json:"end_balance"`
        Transactions    []LedgerEntry `json:"transactions"`
        Summary struct {
            TotalCredits       float64 `json:"total_credits"`
            TotalDebits        float64 `json:"total_debits"`
            TotalTransactions  int     `json:"total_transactions"`
        } `json:"summary"`
    }

    // Set formatted dates
    statement.StartDate = startDate.Format(time.RFC3339)
    statement.EndDate = endDate.Format(time.RFC3339)

    // Fetch account information
    err = s.db.QueryRow(c.Request.Context(), 
        "SELECT id, balance, created_at FROM accounts WHERE id = $1", 
        accountID).Scan(&statement.AccountInfo.ID, &statement.AccountInfo.Balance, &statement.AccountInfo.CreatedAt)
    if err != nil {
        if err == pgx.ErrNoRows {
            c.JSON(404, gin.H{"error": "Account not found"})
        } else {
            c.JSON(500, gin.H{"error": "Failed to retrieve account information"})
        }
        return
    }

    // Find start balance (balance before first transaction in the date range)
    startBalanceQuery := bson.M{
        "account_id": accountID,
        "created_at": bson.M{"$lt": startDate},
    }
    var lastEntryBeforeStartDate LedgerEntry
    err = s.mongo.Database("banking").Collection("ledger").
        FindOne(c.Request.Context(), startBalanceQuery, 
            options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}})).
        Decode(&lastEntryBeforeStartDate)

    if err == nil {
        statement.StartBalance = lastEntryBeforeStartDate.NewBalance
    } else if err == mongo.ErrNoDocuments {
        // If no previous entries, use current account balance as start balance
        statement.StartBalance = statement.AccountInfo.Balance
    } else {
        c.JSON(500, gin.H{"error": "Failed to retrieve start balance"})
        return
    }

    // Fetch transactions within date range
    filter := bson.M{
        "account_id": accountID,
        "created_at": bson.M{
            "$gte": startDate,
            "$lte": endDate,
        },
    }
    opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})

    cursor, err := s.mongo.Database("banking").Collection("ledger").Find(c.Request.Context(), filter, opts)
    if err != nil {
        c.JSON(500, gin.H{"error": "Failed to retrieve transactions"})
        return
    }
    defer cursor.Close(c.Request.Context())

    // Decode transactions
    if err = cursor.All(c.Request.Context(), &statement.Transactions); err != nil {
        c.JSON(500, gin.H{"error": "Failed to decode transactions"})
        return
    }

    // Calculate summary
    statement.EndBalance = statement.StartBalance
    for _, tx := range statement.Transactions {
        statement.Summary.TotalTransactions++
        
        if tx.Amount > 0 {
            statement.Summary.TotalCredits += tx.Amount
        } else {
            statement.Summary.TotalDebits += math.Abs(tx.Amount)
        }
        
        statement.EndBalance = tx.NewBalance
    }

    c.JSON(200, statement)
}

// Helper function to generate UUID 
func GenerateUUID() string {
    // Generate 16 random bytes
    bytes := make([]byte, 16)
    _, err := rand.Read(bytes)
    if err != nil {
        // Fallback to timestamp + random number if crypto/rand fails
        return fmt.Sprintf("tx-%s-%d", time.Now().Format("20060102150405"), time.Now().UnixNano())
    }
    
    return fmt.Sprintf("tx-%s-%s", time.Now().Format("20060102150405"), hex.EncodeToString(bytes))
}


func (s *Server) transfer(c *gin.Context) {
    var req TransferRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": "Invalid request payload: " + err.Error()})
        return
    }

    // Convert metadata from map[string]interface{} to map[string]string
    stringMetadata := make(map[string]string)
    for k, v := range req.Metadata {
        // Convert interface{} to string
        if str, ok := v.(string); ok {
            stringMetadata[k] = str
        } else {
            // If value can't be converted to string, use string representation
            stringMetadata[k] = fmt.Sprint(v)
        }
    }

    // Create TransactionRequest from TransferRequest
    txReq := TransactionRequest{
        IdempotencyKey: req.IdempotencyKey,
        Amount:         req.Amount,
        Description:    req.Description,
        Metadata:       stringMetadata,  // Now using converted string metadata
    }

    // Process the transfer
    ledgerEntry, err := s.processTransaction(
        c.Request.Context(),
        req.FromAccountID,
        req.ToAccountID,
        req.Amount,
        "transfer",
        txReq,
    )

    if err != nil {
        switch {
        case err.Error() == "from account not found" || err.Error() == "to account not found":
            c.JSON(404, gin.H{"error": err.Error()})
        case err.Error() == "insufficient funds in source account":
            c.JSON(400, gin.H{"error": err.Error()})
        default:
            c.JSON(500, gin.H{"error": err.Error()})
        }
        return
    }

    c.JSON(201, ledgerEntry)
}


type TransferRequest struct {
    FromAccountID   string                 `json:"fromAccountID" binding:"required"`
    ToAccountID     string                 `json:"toAccountID" binding:"required"`  // Changed from to_account_id
    Amount          float64                `json:"amount" binding:"required,gt=0"`
    Description     string                 `json:"description"`
    IdempotencyKey  string                 `json:"idempotencyKey" binding:"required"`  // Changed from idempotency_key
    Metadata        map[string]interface{} `json:"metadata"`
}

type TransferResult struct {
    Status   int         `json:"status"`
    Response interface{} `json:"response"`
}