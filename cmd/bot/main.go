package main

import (
	"fmt"

	"github.com/shopspring/decimal"
)

func main() {
	price := decimal.NewFromFloat(64250.50)
	quantity := decimal.NewFromFloat(0.015)

	totalValue := price.Mul(quantity)

	fmt.Printf("Executing Trade Value: %s USDT\n", totalValue.StringFixed(2))
}
