package tools

import (
	"testing"

	appie "github.com/gwillem/appie-go"
)

func TestNewProductSummaryRegularPrice(t *testing.T) {
	p := appie.Product{ID: 1, Title: "Melk", UnitSize: "1 L"}
	p.Price.Now = 1.29

	got := newProductSummary(p)
	if got.Price != 1.29 {
		t.Fatalf("Price = %v, want 1.29", got.Price)
	}
	if got.BonusPrice != 0 {
		t.Fatalf("BonusPrice = %v, want 0 for a non-bonus product", got.BonusPrice)
	}
	if got.IsBonus {
		t.Fatal("IsBonus should be false")
	}
}

// On promotion, Price.Now is what you pay and Price.Was is the original.
func TestNewProductSummaryBonusPrice(t *testing.T) {
	p := appie.Product{ID: 2, Title: "Kaas", IsBonus: true, BonusMechanism: "2+1 gratis"}
	p.Price.Now = 4.00
	p.Price.Was = 6.00

	got := newProductSummary(p)
	if got.Price != 6.00 {
		t.Fatalf("Price = %v, want the pre-promotion 6.00", got.Price)
	}
	if got.BonusPrice != 4.00 {
		t.Fatalf("BonusPrice = %v, want 4.00", got.BonusPrice)
	}
	if got.BonusMechanism != "2+1 gratis" {
		t.Fatalf("BonusMechanism = %q", got.BonusMechanism)
	}
}

// Some bonus products come back without a "was" price; falling back to the
// current price avoids reporting a price of 0.
func TestNewProductSummaryBonusWithoutWasPrice(t *testing.T) {
	p := appie.Product{ID: 3, Title: "Brood", IsBonus: true}
	p.Price.Now = 2.50

	if got := newProductSummary(p); got.Price != 2.50 {
		t.Fatalf("Price = %v, want the 2.50 fallback", got.Price)
	}
}

func TestProductSummariesImageToggle(t *testing.T) {
	p := appie.Product{ID: 4, Title: "Appel", Images: []appie.Image{{URL: "https://img/1.png"}}}

	with := productSummaries([]appie.Product{p}, true)
	if with[0].ImageURL != "https://img/1.png" {
		t.Fatalf("ImageURL = %q, want the first image", with[0].ImageURL)
	}

	without := productSummaries([]appie.Product{p}, false)
	if without[0].ImageURL != "" {
		t.Fatalf("ImageURL = %q, want it dropped for bulk results", without[0].ImageURL)
	}
}

func TestProductSummariesEmptyIsNonNil(t *testing.T) {
	// A nil slice marshals to "null"; an empty one to "[]", which reads far
	// better for a model consuming the result.
	if got := productSummaries(nil, true); got == nil {
		t.Fatal("productSummaries(nil) should return an empty, non-nil slice")
	}
}

func TestNewProductDetailSharesPricingWithSummary(t *testing.T) {
	p := &appie.Product{ID: 5, Title: "Yoghurt", IsBonus: true, Brand: "AH"}
	p.Price.Now = 1.00
	p.Price.Was = 2.00
	p.NutritionalInfo = []appie.NutritionalInfo{{Name: "Energy", Value: "100 kcal"}}

	withNutri := newProductDetail(p, true)
	if withNutri.Price != 2.00 || withNutri.BonusPrice != 1.00 {
		t.Fatalf("pricing = %v/%v, want 2.00/1.00", withNutri.Price, withNutri.BonusPrice)
	}
	if withNutri.NutritionalInfo == nil {
		t.Fatal("nutritional info requested but missing")
	}

	if withoutNutri := newProductDetail(p, false); withoutNutri.NutritionalInfo != nil {
		t.Fatal("nutritional info should be omitted unless requested")
	}
}
