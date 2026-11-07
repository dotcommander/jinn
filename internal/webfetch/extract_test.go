package webfetch

import (
	"strings"
	"testing"
)

func TestExtractFieldsGroundedLine(t *testing.T) {
	t.Parallel()
	content := strings.Join([]string{
		"# Product page",
		"Price: $1,299.50 today",
		"In stock: yes",
		"Rating: 4.5 stars",
	}, "\n")
	results := ExtractFields(content, map[string]string{
		"price":   "number",
		"stock":   "boolean",
		"rating":  "number",
		"missing": "string",
	})
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	byField := map[string]FieldResult{}
	for _, result := range results {
		byField[result.Field] = result
	}
	price := byField["price"]
	if price.Status != FieldFound || price.Value != 1299.5 || price.Line != 2 || price.Source != "Price: $1,299.50 today" {
		t.Fatalf("price = %+v", price)
	}
	if stock := byField["stock"]; stock.Status != FieldFound || stock.Value != true || stock.Line != 3 {
		t.Fatalf("stock = %+v", stock)
	}
	if missing := byField["missing"]; missing.Status != FieldNotOnPage {
		t.Fatalf("missing = %+v", missing)
	}
}

func TestExtractFieldsUncertainOnTypeMismatch(t *testing.T) {
	t.Parallel()
	results := ExtractFields("Price: free today", map[string]string{"price": "number"})
	if len(results) != 1 || results[0].Status != FieldUncertain || results[0].Line != 1 {
		t.Fatalf("results = %+v", results)
	}
}
