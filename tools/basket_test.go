package tools

import "testing"

func productItem(pid int, title, desc, itemType, origin string) v2ListItem {
	it := v2ListItem{Description: desc, Type: itemType, OriginCode: origin}
	it.ProductDetails.Product.WebshopID = pid
	it.ProductDetails.Product.Title = title
	return it
}

func TestV2ListItemName(t *testing.T) {
	if got := productItem(1, "Melk halfvol", "", "", "").name(); got != "Melk halfvol" {
		t.Fatalf("name = %q, want the product title when description is blank", got)
	}
	if got := productItem(1, "Melk halfvol", "verse bloemen", "", "").name(); got != "verse bloemen" {
		t.Fatalf("name = %q, want the description when it is set", got)
	}
	if got := (v2ListItem{}).name(); got != "" {
		t.Fatalf("name = %q, want empty for an item with neither", got)
	}
}

// AH rejects the removal PATCH when description, type or originCode are blank,
// so each has to fall back to what the app would have sent.
func TestRemovalPatchFillsRequiredFields(t *testing.T) {
	got := removalPatch(productItem(12345, "Melk halfvol", "", "", ""))

	if got.Quantity != 0 {
		t.Fatalf("Quantity = %d, want 0 (that is what signals deletion)", got.Quantity)
	}
	if got.ProductID != 12345 {
		t.Fatalf("ProductID = %d, want 12345", got.ProductID)
	}
	if got.Description != "Melk halfvol" {
		t.Fatalf("Description = %q, want the product title fallback", got.Description)
	}
	if got.SearchTerm != "Melk halfvol" {
		t.Fatalf("SearchTerm = %q, want it to mirror the description", got.SearchTerm)
	}
	if got.Type != "SHOPPABLE" {
		t.Fatalf("Type = %q, want the SHOPPABLE fallback", got.Type)
	}
	if got.OriginCode != "PRD" {
		t.Fatalf("OriginCode = %q, want the PRD fallback", got.OriginCode)
	}
	if got.StrikeThrough {
		t.Fatal("StrikeThrough should be false on removal")
	}
}

func TestRemovalPatchPreservesExistingFields(t *testing.T) {
	got := removalPatch(productItem(9, "Title", "verse bloemen", "FREE_TEXT", "MAN"))

	if got.Description != "verse bloemen" || got.SearchTerm != "verse bloemen" {
		t.Fatalf("description/searchTerm = %q/%q, want the original description", got.Description, got.SearchTerm)
	}
	if got.Type != "FREE_TEXT" {
		t.Fatalf("Type = %q, want the item's own type", got.Type)
	}
	if got.OriginCode != "MAN" {
		t.Fatalf("OriginCode = %q, want the item's own origin", got.OriginCode)
	}
}

// Free-text items carry no product id; omitempty must keep productId out of
// the payload rather than sending 0.
func TestRemovalPatchOmitsZeroProductID(t *testing.T) {
	got := removalPatch(v2ListItem{Description: "verse bloemen", Type: "FREE_TEXT", OriginCode: "MAN"})
	if got.ProductID != 0 {
		t.Fatalf("ProductID = %d, want 0 for a free-text item", got.ProductID)
	}
}
