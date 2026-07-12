package shopify

import (
	"errors"
	"testing"

	goshopify "github.com/bold-commerce/go-shopify/v4"
)

func TestNormalizeProductUpdateError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		message string
	}{
		{
			name:    "远端商品不存在",
			err:     goshopify.ResponseError{Status: 404, Message: "Not Found"},
			message: "This Shopify product no longer exists. Publish it as a new product instead.",
		},
		{
			name:    "普通更新失败保留原因",
			err:     errors.New("validation failed"),
			message: "Failed to update product: validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := normalizeProductUpdateError(tt.err).Error(); actual != tt.message {
				t.Fatalf("normalizeProductUpdateError() = %q, want %q", actual, tt.message)
			}
		})
	}
}
