package tools

import (
	"reflect"
	"testing"
)

func TestToInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{float64(42), 42}, // how encoding/json decodes numbers
		{42, 42},          // native int
		{int64(42), 42},   // native int64
		{"42", 0},         // strings are not numbers here
		{nil, 0},          // absent
		{float64(-3), -3}, // negatives pass through; callers filter
		{map[string]any{}, 0},
	}
	for _, c := range cases {
		if got := toInt(c.in); got != c.want {
			t.Errorf("toInt(%#v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(5, 1, 10); got != 5 {
		t.Errorf("in-range clamp = %d, want 5", got)
	}
	if got := clamp(0, 1, 10); got != 1 {
		t.Errorf("below-range clamp = %d, want 1", got)
	}
	if got := clamp(99, 1, 10); got != 10 {
		t.Errorf("above-range clamp = %d, want 10", got)
	}
}

func TestParseIntArray(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    []int
		wantErr bool
	}{
		{"json string", `[1, 2, 3]`, []int{1, 2, 3}, false},
		{"native array", []any{float64(1), float64(2)}, []int{1, 2}, false},
		{"drops non-positive", `[1, 0, -5, 2]`, []int{1, 2}, false},
		{"absent", nil, nil, false},
		{"empty string", "  ", nil, false},
		{"malformed", `[1, `, nil, true},
		{"wrong type", float64(3), nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseIntArray(c.in, "product_ids")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if !c.wantErr && len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

// A blank name would match every unlabelled shopping-list item and delete far
// more than the caller asked for, so empties must never survive parsing.
func TestParseStringArrayDropsBlanks(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"json string with blanks", `["melk", "", "  ", "kaas"]`, []string{"melk", "kaas"}},
		{"native array with blanks", []any{"melk", "", "  "}, []string{"melk"}},
		{"only blanks", `["", " "]`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseStringArray(c.in, "names")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestParseStringArrayErrors(t *testing.T) {
	if _, err := parseStringArray(`["a"`, "names"); err == nil {
		t.Fatal("malformed JSON should error")
	}
	if _, err := parseStringArray(float64(1), "names"); err == nil {
		t.Fatal("wrong type should error")
	}
}

func TestParseLineItems(t *testing.T) {
	cases := []struct {
		name       string
		in         any
		defaultQty int
		want       []lineItem
		wantErr    bool
	}{
		{
			name:       "json string",
			in:         `[{"product_id": 1, "quantity": 2}]`,
			defaultQty: 1,
			want:       []lineItem{{ProductID: 1, Quantity: 2}},
		},
		{
			name:       "native array",
			in:         []any{map[string]any{"product_id": float64(7), "quantity": float64(3)}},
			defaultQty: 1,
			want:       []lineItem{{ProductID: 7, Quantity: 3}},
		},
		{
			name:       "missing quantity gets default",
			in:         `[{"product_id": 5}]`,
			defaultQty: 1,
			want:       []lineItem{{ProductID: 5, Quantity: 1}},
		},
		{
			name:       "zero quantity preserved when it means remove",
			in:         `[{"product_id": 5, "quantity": 0}]`,
			defaultQty: 0,
			want:       []lineItem{{ProductID: 5, Quantity: 0}},
		},
		{
			name:       "drops invalid product ids",
			in:         `[{"product_id": 0, "quantity": 2}, {"product_id": -1}, {"product_id": 9, "quantity": 1}]`,
			defaultQty: 1,
			want:       []lineItem{{ProductID: 9, Quantity: 1}},
		},
		{
			name:       "skips non-object entries",
			in:         []any{"nope", map[string]any{"product_id": float64(4), "quantity": float64(1)}},
			defaultQty: 1,
			want:       []lineItem{{ProductID: 4, Quantity: 1}},
		},
		{name: "malformed", in: `[{`, defaultQty: 1, wantErr: true},
		{name: "wrong type", in: float64(2), defaultQty: 1, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLineItems(c.in, "items", c.defaultQty)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestToListItems(t *testing.T) {
	got := toListItems([]lineItem{{ProductID: 1, Quantity: 2}, {ProductID: 3, Quantity: 4}})
	if len(got) != 2 || got[0].ProductID != 1 || got[0].Quantity != 2 || got[1].ProductID != 3 {
		t.Fatalf("toListItems = %+v", got)
	}
}
