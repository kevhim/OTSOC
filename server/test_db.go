package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	passwords := []string{"password", "development_password"}
	for _, p := range passwords {
		u := fmt.Sprintf("postgres://root:%s@localhost:5432/redcyberfox", p)
		pool, err := pgxpool.New(context.Background(), u)
		if err != nil {
			fmt.Println("Error parsing", p, ":", err)
			continue
		}
		err = pool.Ping(context.Background())
		if err == nil {
			fmt.Println("SUCCESS with password:", p)
			return
		} else {
			fmt.Println("FAIL with password:", p, ":", err)
		}
	}
}
