-- 固定形态查询(sqlc-gen-go 承担);动态 List 由 sqlc_gen_dynamic 插件承担。

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateUser :one
INSERT INTO users (name, email, password, status, role, age, score, balance,
                   birth_date, org_id, tags, meta)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET name = $2, email = $3, role = $4, age = $5, score = $6, balance = $7,
    birth_date = $8, org_id = $9, tags = $10, meta = $11, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetUserPassword :execrows
UPDATE users SET password = $2 WHERE id = $1;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: CreateOrder :one
INSERT INTO orders (order_no, user_id, amount_cents, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateOrder :one
UPDATE orders
SET order_no = $2, user_id = $3, amount_cents = $4, status = $5
WHERE id = $1
RETURNING *;

-- name: DeleteOrder :execrows
DELETE FROM orders WHERE id = $1;
