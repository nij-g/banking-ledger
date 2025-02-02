
# Banking Service API

This API allows you to manage bank accounts, including creating accounts, depositing and withdrawing money, transferring funds, and tracking account transactions.

## API Endpoints

### 1️⃣ Create an Account
- **Method**: `POST /accounts`
- **Request**:
  ```json
  {
    "id": "test-account",
    "balance": 1000.00
  }
  ```
- **Response**:
  ```json
  {
    "id": "test-account",
    "balance": 1000.00
  }
  ```

### 2️⃣ Get Account Details
- **Method**: `GET /accounts/:id`
- **Example**: `/accounts/test-account`
- **Response**:
  ```json
  {
    "id": "test-account",
    "balance": 1000.00
  }
  ```

### 3️⃣ Deposit Money
- **Method**: `POST /accounts/:id/deposit`
- **Request**:
  ```json
  {
    "amount": 500.00,
    "idempotencyKey": "unique-key-123"
  }
  ```
- **Response**:
  ```json
  {
    "transaction_id": "txn123",
    "new_balance": 1500.00
  }
  ```

### 4️⃣ Withdraw Money
- **Method**: `POST /accounts/:id/withdraw`
- **Request**:
  ```json
  {
    "amount": 200.00,
    "idempotencyKey": "unique-key-456"
  }
  ```
- **Response**:
  ```json
  {
    "transaction_id": "txn124",
    "new_balance": 1300.00
  }
  ```

### 5️⃣ Transfer Funds Between Accounts
- **Method**: `POST /accounts/transfer`
- **Request**:
  ```json
  {
    "fromAccountID": "test-account",
    "toAccountID": "receiver-account",
    "amount": 100.00,
    "idempotencyKey": "transfer-789",
    "description": "Test Transfer"
  }
  ```
- **Response**:
  ```json
  {
    "transaction_id": "txn125",
    "from_balance": 1200.00,
    "to_balance": 1100.00
  }
  ```

### 6️⃣ Get Account Transactions
- **Method**: `GET /accounts/:id/transactions`
- **Response**:
  ```json
  [
    {
      "transaction_id": "txn123",
      "type": "deposit",
      "amount": 500.00,
      "balance_after": 1500.00
    },
    {
      "transaction_id": "txn124",
      "type": "withdrawal",
      "amount": 200.00,
      "balance_after": 1300.00
    }
  ]
  ```

---

## Running Tests

### 1️⃣ Run Unit Tests
To run the unit tests, use the following command:
```bash
docker-compose run --rm tests
```
**Expected Output**:
```bash
=== RUN   TestCreateAccount
--- PASS: TestCreateAccount (0.05s)
=== RUN   TestTransferFunds
--- PASS: TestTransferFunds (0.07s)
PASS
ok      app    0.200s
```

### 2️⃣ Run Integration Tests
To run the integration tests, use the following command:
```bash
docker-compose run --rm integration-tests
```
**Expected Output**:
```bash
=== RUN   TestIntegration_CreateAccount
--- PASS: TestIntegration_CreateAccount (0.10s)
=== RUN   TestIntegration_Transfer
--- PASS: TestIntegration_Transfer (0.07s)
PASS
ok      app    0.500s
```

---

## Troubleshooting

### 1️⃣ Check Logs for Errors
To check the logs for errors, use the following command:
```bash
docker logs <app_container_id>
```

### 2️⃣ Reset Docker Services
If you're facing issues, try resetting the Docker services:
```bash
docker-compose down
docker-compose up --build -d
```

### 3️⃣ Check Database Connections

#### PostgreSQL CLI:
To access the PostgreSQL database:
```bash
docker exec -it <postgres_container_id> psql -U postgres -d banking
```
Run the following query:
```sql
SELECT * FROM accounts;
```

#### MongoDB CLI:
To access MongoDB:
```bash
docker exec -it <mongo_container_id> mongo banking
```
Run the following command:
```javascript
db.ledger.find().pretty()
```

#### RabbitMQ Admin Panel:
Open the RabbitMQ Admin Panel at [http://localhost:15672/](http://localhost:15672/) (default user: `guest`, pass: `guest`).

---

## Future Improvements
- ✅ Add JWT Authentication for security.
- ✅ Implement gRPC for microservices.
- ✅ Add GraphQL API support.
- ✅ Implement Kafka instead of RabbitMQ for scalability.
