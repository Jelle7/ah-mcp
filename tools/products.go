package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxSearchLimit     = 30
	maxBulkQueries     = 10
	maxBulkProductIDs  = 20
	searchConcurrency  = 3
	productConcurrency = 5
)

// RegisterProductTools registers product-related MCP tools.
func RegisterProductTools(s *server.MCPServer, deps Deps) {
	registerSearchProducts(s, deps)
	registerSearchProductsBulk(s, deps)
	registerSearchProductsFiltered(s, deps)
	registerGetProduct(s, deps)
	registerGetProductsBulk(s, deps)
	registerGetBonusOffers(s, deps)
	registerGetBonusGroupProducts(s, deps)
	registerGetLastChanceItems(s, deps)
	registerSearchStores(s, deps)
}

// productSummary is the compact product shape returned by the search and
// bonus tools.
type productSummary struct {
	ID             int     `json:"id"`
	Title          string  `json:"title"`
	Price          float64 `json:"price"`
	BonusPrice     float64 `json:"bonus_price,omitempty"`
	Unit           string  `json:"unit,omitempty"`
	IsBonus        bool    `json:"is_bonus"`
	BonusMechanism string  `json:"bonus_mechanism,omitempty"`
	ImageURL       string  `json:"image_url,omitempty"`
}

// newProductSummary normalises AH's pricing, where Price.Now is the amount
// actually charged and Price.Was is only populated while a promotion runs.
func newProductSummary(p appie.Product) productSummary {
	s := productSummary{
		ID:             p.ID,
		Title:          p.Title,
		Unit:           p.UnitSize,
		IsBonus:        p.IsBonus,
		BonusMechanism: p.BonusMechanism,
	}
	if p.IsBonus {
		s.BonusPrice = p.Price.Now
		s.Price = p.Price.Was
		if s.Price == 0 {
			s.Price = p.Price.Now
		}
	} else {
		s.Price = p.Price.Now
	}
	if len(p.Images) > 0 {
		s.ImageURL = p.Images[0].URL
	}
	return s
}

func productSummaries(products []appie.Product, withImages bool) []productSummary {
	out := make([]productSummary, 0, len(products))
	for _, p := range products {
		s := newProductSummary(p)
		if !withImages {
			s.ImageURL = ""
		}
		out = append(out, s)
	}
	return out
}

// productDetail is the full product shape returned by ah_get_product and
// ah_get_products_bulk. Both cache under the same keys, so they must agree on
// the shape or one tool will read back a payload missing the other's fields.
type productDetail struct {
	ID                   int      `json:"id"`
	Title                string   `json:"title"`
	Brand                string   `json:"brand,omitempty"`
	Category             string   `json:"category,omitempty"`
	ShortDescription     string   `json:"short_description,omitempty"`
	Price                float64  `json:"price"`
	BonusPrice           float64  `json:"bonus_price,omitempty"`
	UnitSize             string   `json:"unit_size,omitempty"`
	UnitPriceDescription string   `json:"unit_price_description,omitempty"`
	IsBonus              bool     `json:"is_bonus"`
	BonusMechanism       string   `json:"bonus_mechanism,omitempty"`
	NutriScore           string   `json:"nutri_score,omitempty"`
	IsAvailable          bool     `json:"is_available"`
	PropertyIcons        []string `json:"property_icons,omitempty"`
	NutritionalInfo      any      `json:"nutritional_info,omitempty"`
	ImageURL             string   `json:"image_url,omitempty"`
	Error                string   `json:"error,omitempty"`
}

func newProductDetail(p *appie.Product, includeNutri bool) productDetail {
	summary := newProductSummary(*p)
	d := productDetail{
		ID:                   p.ID,
		Title:                p.Title,
		Brand:                p.Brand,
		Category:             p.Category,
		ShortDescription:     p.ShortDescription,
		Price:                summary.Price,
		BonusPrice:           summary.BonusPrice,
		UnitSize:             p.UnitSize,
		UnitPriceDescription: p.UnitPriceDescription,
		IsBonus:              p.IsBonus,
		BonusMechanism:       p.BonusMechanism,
		NutriScore:           p.NutriScore,
		IsAvailable:          p.IsAvailable,
		PropertyIcons:        p.PropertyIcons,
		ImageURL:             summary.ImageURL,
	}
	if includeNutri && len(p.NutritionalInfo) > 0 {
		d.NutritionalInfo = p.NutritionalInfo
	}
	return d
}

// fetchProduct loads one product, honouring the shared cache and retrying on
// AH rate limits.
func fetchProduct(ctx context.Context, c *appie.Client, tool string, id int, includeNutri bool) (productDetail, error) {
	cacheKey := ProductCacheKey(id)
	if includeNutri {
		cacheKey = ProductFullCacheKey(id)
	}
	if cached, ok := GlobalCache.Get(cacheKey); ok {
		var d productDetail
		if json.Unmarshal(cached, &d) == nil {
			return d, nil
		}
	}

	var p *appie.Product
	if err := withRetry(ctx, tool, func() error {
		var e error
		if includeNutri {
			p, e = c.GetProductFull(ctx, id)
		} else {
			p, e = c.GetProduct(ctx, id)
		}
		return e
	}); err != nil {
		return productDetail{}, err
	}

	d := newProductDetail(p, includeNutri)
	if data, err := json.Marshal(d); err == nil {
		GlobalCache.Set(cacheKey, data, CacheTTLProduct)
	}
	return d, nil
}

// --- ah_search_products ---

func registerSearchProducts(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products",
		mcp.WithTitleAnnotation("Albert Heijn: Search Products"),
		mcp.WithDescription(
			"Search for Albert Heijn (Dutch supermarket) products by keyword. "+
				"AH is a Dutch supermarket so prefer Dutch search terms for best results: "+
				"e.g. 'melk' (milk), 'kaas' (cheese), 'brood' (bread), 'kip' (chicken), 'appel' (apple). "+
				"English terms also work but may return fewer results. "+
				"Returns id, title, price, bonus_price, unit, is_bonus, image_url.",
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query in Dutch or English, e.g. 'melk', 'cola', 'pindakaas', 'chicken'"),
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results to return (default 10, max 30)"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := strings.TrimSpace(req.GetString("query", ""))
		if query == "" {
			return errResult("query is required"), nil
		}
		limit := clamp(req.GetInt("limit", 10), 1, maxSearchLimit)
		start := time.Now()

		cacheKey := SearchCacheKey(query, limit)
		if cached, ok := GlobalCache.Get(cacheKey); ok {
			LogInfo("ah_search_products", "cache_hit query=%q duration=%v", query, time.Since(start))
			return mcp.NewToolResultText(string(cached)), nil
		}

		var products []appie.Product
		if err := withRetry(ctx, "ah_search_products", func() error {
			var e error
			products, e = c.SearchProducts(ctx, query, limit)
			return e
		}); err != nil {
			LogError("ah_search_products", "search failed query=%q err=%v", query, err)
			return errResult(fmt.Sprintf("Search failed: %v", err)), nil
		}

		results := productSummaries(products, true)
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return errResult(fmt.Sprintf("marshal result: %v", err)), nil
		}
		GlobalCache.Set(cacheKey, data, CacheTTLSearch)
		LogInfo("ah_search_products", "query=%q results=%d duration=%v", query, len(results), time.Since(start))
		return mcp.NewToolResultText(string(data)), nil
	}))
}

// --- ah_search_products_bulk ---

func registerSearchProductsBulk(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products_bulk",
		mcp.WithTitleAnnotation("Albert Heijn: Bulk Product Search"),
		mcp.WithDescription(
			"Search for multiple products in one tool call. "+
				"Pass a JSON array of search queries; results for all are returned together. "+
				"Use this instead of calling ah_search_products repeatedly — saves tool-call quota. "+
				"Max 10 queries per call. Dutch terms give best results.",
		),
		mcp.WithString("queries",
			mcp.Required(),
			mcp.Description(`JSON array of search queries, e.g. ["melk", "kaas", "brood", "kip"]`),
		),
		mcp.WithString("limit",
			mcp.Description("Max results per query (default 5, max 10)"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		queries, err := parseStringArray(req.GetArguments()["queries"], "queries")
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(queries) == 0 {
			return errResult("queries array is empty"), nil
		}
		if len(queries) > maxBulkQueries {
			queries = queries[:maxBulkQueries]
		}
		limit := clamp(req.GetInt("limit", 5), 1, 10)
		start := time.Now()

		type searchResult struct {
			Query   string           `json:"query"`
			Results []productSummary `json:"results"`
			Error   string           `json:"error,omitempty"`
		}
		results := make([]searchResult, len(queries))

		sem := make(chan struct{}, searchConcurrency)
		var wg sync.WaitGroup
		for i, q := range queries {
			wg.Add(1)
			go func(i int, q string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				results[i].Query = q

				cacheKey := SearchCacheKey(q, limit)
				if cached, ok := GlobalCache.Get(cacheKey); ok {
					var items []productSummary
					if json.Unmarshal(cached, &items) == nil {
						results[i].Results = items
						return
					}
				}

				var products []appie.Product
				if err := withRetry(ctx, "ah_search_products_bulk", func() error {
					var e error
					products, e = c.SearchProducts(ctx, q, limit)
					return e
				}); err != nil {
					results[i].Error = err.Error()
					return
				}

				items := productSummaries(products, false)
				results[i].Results = items
				// Cached under the plain search key so ah_search_products can
				// reuse it; it is marshalled with the same shape.
				if data, err := json.MarshalIndent(items, "", "  "); err == nil {
					GlobalCache.Set(cacheKey, data, CacheTTLSearch)
				}
			}(i, q)
		}
		wg.Wait()

		LogInfo("ah_search_products_bulk", "queries=%d duration=%v", len(queries), time.Since(start))
		return jsonResult(results)
	}))
}

// --- ah_search_products_filtered ---

func registerSearchProductsFiltered(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products_filtered",
		mcp.WithTitleAnnotation("Albert Heijn: Search Products (Filtered)"),
		mcp.WithDescription(
			"Search Albert Heijn products with optional bonus filter. "+
				"Set bonus=true to return only products currently on promotion/sale. "+
				"Dutch search terms give best results: 'melk', 'kaas', 'vlees', etc.",
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query in Dutch or English"),
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results (default 10, max 30)"),
		),
		mcp.WithString("bonus",
			mcp.Description("Set to 'true' to return only products currently on bonus/promotion"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := strings.TrimSpace(req.GetString("query", ""))
		if query == "" {
			return errResult("query is required"), nil
		}
		limit := clamp(req.GetInt("limit", 10), 1, maxSearchLimit)
		bonus := req.GetString("bonus", "") == "true"

		var products []appie.Product
		if err := withRetry(ctx, "ah_search_products_filtered", func() error {
			var e error
			products, e = c.SearchProductsFiltered(ctx, appie.SearchOptions{
				Query: query,
				Limit: limit,
				Bonus: bonus,
			})
			return e
		}); err != nil {
			LogError("ah_search_products_filtered", "search failed query=%q err=%v", query, err)
			return errResult(fmt.Sprintf("Search failed: %v", err)), nil
		}
		return jsonResult(productSummaries(products, true))
	}))
}

// --- ah_get_product ---

func registerGetProduct(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_product",
		mcp.WithTitleAnnotation("Albert Heijn: Product Details"),
		mcp.WithDescription(
			"Get detailed information about a single Albert Heijn product by ID. "+
				"Returns title, brand, category, description, price, unit size, bonus info, NutriScore, property icons. "+
				"Set include_nutritional_info=true to also return calories, fat, protein, etc. "+
				"Get product_id from ah_search_products.",
		),
		mcp.WithString("product_id",
			mcp.Required(),
			mcp.Description("Numeric product ID from ah_search_products"),
		),
		mcp.WithString("include_nutritional_info",
			mcp.Description("Set to 'true' to include nutritional values (default false)"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		productID := req.GetInt("product_id", 0)
		if productID <= 0 {
			return errResult("product_id is required"), nil
		}
		includeNutri := req.GetString("include_nutritional_info", "") == "true"
		start := time.Now()

		detail, err := fetchProduct(ctx, c, "ah_get_product", productID, includeNutri)
		if err != nil {
			LogError("ah_get_product", "failed id=%d err=%v", productID, err)
			return errResult(fmt.Sprintf("Failed to get product %d: %v", productID, err)), nil
		}
		LogInfo("ah_get_product", "id=%d nutri=%v duration=%v", productID, includeNutri, time.Since(start))
		return jsonResult(detail)
	}))
}

// --- ah_get_products_bulk ---

func registerGetProductsBulk(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_products_bulk",
		mcp.WithTitleAnnotation("Albert Heijn: Bulk Product Details"),
		mcp.WithDescription(
			"Get details for multiple products by ID in one tool call. "+
				"Use instead of calling ah_get_product repeatedly — saves tool-call quota. "+
				"Optionally include nutritional info (calories, fat, protein, etc.) for all products. "+
				"Max 20 product IDs per call.",
		),
		mcp.WithString("product_ids",
			mcp.Required(),
			mcp.Description("JSON array of product IDs, e.g. [123456, 789012, 345678]"),
		),
		mcp.WithString("include_nutritional_info",
			mcp.Description("Set to 'true' to include nutritional values for all products (default false)"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		productIDs, err := parseIntArray(req.GetArguments()["product_ids"], "product_ids")
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(productIDs) == 0 {
			return errResult("product_ids array is empty"), nil
		}
		if len(productIDs) > maxBulkProductIDs {
			productIDs = productIDs[:maxBulkProductIDs]
		}
		includeNutri := req.GetString("include_nutritional_info", "") == "true"
		start := time.Now()

		results := make([]productDetail, len(productIDs))
		sem := make(chan struct{}, productConcurrency)
		var wg sync.WaitGroup
		for i, pid := range productIDs {
			wg.Add(1)
			go func(i, pid int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				detail, err := fetchProduct(ctx, c, "ah_get_products_bulk", pid, includeNutri)
				if err != nil {
					results[i] = productDetail{ID: pid, Error: err.Error()}
					return
				}
				results[i] = detail
			}(i, pid)
		}
		wg.Wait()

		LogInfo("ah_get_products_bulk", "ids=%d nutri=%v duration=%v", len(productIDs), includeNutri, time.Since(start))
		return jsonResult(results)
	}))
}

// --- ah_get_bonus_offers ---

func registerGetBonusOffers(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_bonus_offers",
		mcp.WithTitleAnnotation("Albert Heijn: Bonus Offers"),
		mcp.WithDescription(
			"Get current Albert Heijn bonus/promotional offers. "+
				"Use this (not ah_search_products) when the user asks what is on bonus/sale/discount. "+
				"Supports optional keyword filter to find e.g. cheese on bonus: set query='kaas'. "+
				"Group deals (e.g. '2+1 gratis', 'Alle yoghurt 25% korting') have id=0 and a non-empty bonus_segment_id — "+
				"pass that to ah_get_bonus_group_products to see the individual products in the group. "+
				"Returns id, bonus_segment_id, title, original_price, bonus_price, discount_percentage, bonus_mechanism.",
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results to return (default 20, max 100)"),
		),
		mcp.WithString("query",
			mcp.Description("Optional keyword filter (Dutch or English) applied client-side, e.g. 'kaas', 'vlees', 'bier'"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := clamp(req.GetInt("limit", 20), 1, 100)
		query := strings.ToLower(strings.TrimSpace(req.GetString("query", "")))

		products, err := cachedBonusProducts(ctx, c)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get bonus products: %v", err)), nil
		}

		type item struct {
			ID                 int     `json:"id,omitempty"`
			BonusSegmentID     string  `json:"bonus_segment_id,omitempty"`
			Title              string  `json:"title"`
			OriginalPrice      float64 `json:"original_price,omitempty"`
			BonusPrice         float64 `json:"bonus_price"`
			DiscountPercentage float64 `json:"discount_percentage,omitempty"`
			BonusMechanism     string  `json:"bonus_mechanism,omitempty"`
		}
		results := make([]item, 0, limit)
		for _, p := range products {
			if len(results) >= limit {
				break
			}
			// Client-side keyword filter when query is set.
			if query != "" && !strings.Contains(strings.ToLower(p.Title), query) {
				continue
			}
			it := item{
				ID:             p.ID,
				BonusSegmentID: p.BonusSegmentID,
				Title:          p.Title,
				OriginalPrice:  p.Price.Was,
				BonusPrice:     p.Price.Now,
				BonusMechanism: p.BonusMechanism,
			}
			if p.Price.Was > 0 && p.Price.Now > 0 {
				it.DiscountPercentage = math.Round((1-p.Price.Now/p.Price.Was)*1000) / 10
			}
			results = append(results, it)
		}
		return jsonResult(results)
	}))
}

// cachedBonusProducts returns the current bonus catalogue, falling back to the
// spotlight (featured) deals because GetBonusProducts fans out over every
// category and fails if any single one errors.
func cachedBonusProducts(ctx context.Context, c *appie.Client) ([]appie.Product, error) {
	const cacheKey = "bonus:all"
	if cached, ok := GlobalCache.Get(cacheKey); ok {
		var products []appie.Product
		if json.Unmarshal(cached, &products) == nil {
			return products, nil
		}
	}

	var products []appie.Product
	err := withRetry(ctx, "ah_get_bonus_offers", func() error {
		var e error
		products, e = c.GetBonusProducts(ctx)
		return e
	})
	if err != nil {
		LogWarn("ah_get_bonus_offers", "GetBonusProducts failed (%v), falling back to spotlight", err)
		products, err = c.GetSpotlightBonusProducts(ctx)
		if err != nil {
			return nil, err
		}
	}

	if data, mErr := json.Marshal(products); mErr == nil {
		GlobalCache.Set(cacheKey, data, CacheTTLBonus)
	}
	return products, nil
}

// --- ah_get_bonus_group_products ---

func registerGetBonusGroupProducts(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_bonus_group_products",
		mcp.WithTitleAnnotation("Albert Heijn: Bonus Group Products"),
		mcp.WithDescription(
			"Get all individual products belonging to a specific Albert Heijn bonus promotion group. "+
				"Use this to drill into a deal like '2+1 gratis kaas' or 'Alle yoghurt 25% korting'. "+
				"Get segment_id from the bonus_segment_id field in ah_get_bonus_offers results. "+
				"Returns the same fields as ah_search_products.",
		),
		mcp.WithString("segment_id",
			mcp.Required(),
			mcp.Description("Bonus segment ID from the bonus_segment_id field in ah_get_bonus_offers"),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		segmentID := strings.TrimSpace(req.GetString("segment_id", ""))
		if segmentID == "" {
			return errResult("segment_id is required"), nil
		}

		var products []appie.Product
		if err := withRetry(ctx, "ah_get_bonus_group_products", func() error {
			var e error
			products, e = c.GetBonusGroupProducts(ctx, segmentID)
			return e
		}); err != nil {
			return errResult(fmt.Sprintf("Failed to get bonus group products for %s: %v", segmentID, err)), nil
		}
		return jsonResult(productSummaries(products, true))
	}))
}

// --- ah_get_last_chance_items ---

func registerGetLastChanceItems(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_last_chance_items",
		mcp.WithTitleAnnotation("Albert Heijn: Last-Chance Items"),
		mcp.WithDescription(
			"Get last-chance / vandaag-af / clearance items from an Albert Heijn store. "+
				"Requires a store_id (use ah_search_stores to find one, or provide postal_code to resolve the nearest store). "+
				"Uses the dedicated bargainItems GraphQL endpoint which returns today-only markdown deals.",
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results to return (default 20, max 100)"),
		),
		mcp.WithString("store_id",
			mcp.Description("AH store ID (integer). Required to retrieve bargain items."),
		),
		mcp.WithString("postal_code",
			mcp.Description("Dutch postal code (e.g. '1234AB') to find the nearest store when store_id is not known."),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := clamp(req.GetInt("limit", 20), 1, 100)
		storeID := req.GetInt("store_id", 0)

		// If store_id not provided, try to resolve from postal_code.
		if storeID <= 0 {
			postalCode := strings.TrimSpace(req.GetString("postal_code", ""))
			if postalCode == "" {
				// Try member's address as last resort.
				if member, mErr := c.GetMember(ctx); mErr == nil {
					postalCode = member.Address.PostalCode
				}
			}
			if postalCode != "" {
				if stores, sErr := cachedStoreSearch(ctx, c, postalCode); sErr == nil && len(stores) > 0 {
					storeID = stores[0].ID
				}
			}
		}

		if storeID <= 0 {
			// The bargainItems GraphQL endpoint is store-specific and requires
			// a store ID. Without one we cannot retrieve last-chance items.
			return errResult(
				"Cannot retrieve last-chance items without a store. " +
					"Please provide store_id or postal_code. " +
					"Example: {\"store_id\": 1234} or {\"postal_code\": \"1011AB\"}",
			), nil
		}

		var bargains []appie.Bargain
		if err := withRetry(ctx, "ah_get_last_chance_items", func() error {
			var e error
			bargains, e = c.GetBargains(ctx, storeID)
			return e
		}); err != nil {
			return errResult(fmt.Sprintf("Failed to get bargains for store %d: %v", storeID, err)), nil
		}

		type item struct {
			ID                 int     `json:"id"`
			Title              string  `json:"title"`
			Brand              string  `json:"brand,omitempty"`
			Category           string  `json:"category,omitempty"`
			MarkdownType       string  `json:"markdown_type,omitempty"`
			DiscountPercentage float64 `json:"discount_percentage,omitempty"`
			ExpirationDate     string  `json:"expiration_date,omitempty"`
			Stock              int     `json:"stock,omitempty"`
			PriceWas           string  `json:"price_was,omitempty"`
			PriceNow           string  `json:"price_now"`
		}
		results := make([]item, 0, min(limit, len(bargains)))
		for i, b := range bargains {
			if i >= limit {
				break
			}
			// Parse expiration date for display
			expDate := b.ExpirationDate
			if t, err := time.Parse(time.RFC3339, expDate); err == nil {
				expDate = t.Format("2006-01-02")
			}
			results = append(results, item{
				ID:                 b.Product.ID,
				Title:              b.Product.Title,
				Brand:              b.Product.Brand,
				Category:           b.Category,
				MarkdownType:       b.MarkdownType,
				DiscountPercentage: b.MarkdownPercentage,
				ExpirationDate:     expDate,
				Stock:              b.Stock,
				PriceWas:           b.PriceWas,
				PriceNow:           b.PriceNow,
			})
		}
		return jsonResult(results)
	}))
}

// --- ah_search_stores ---

func registerSearchStores(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_stores",
		mcp.WithTitleAnnotation("Albert Heijn: Search Stores"),
		mcp.WithDescription(
			"Find Albert Heijn stores near a Dutch postal code. "+
				"If no postal_code is given, automatically uses the address from the member profile. "+
				"Returns store id (use this for ah_get_last_chance_items), name, type, and address.",
		),
		mcp.WithString("postal_code",
			mcp.Description("Dutch postal code, e.g. '1234AB'. Optional — falls back to member address."),
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		postalCode := strings.TrimSpace(req.GetString("postal_code", ""))

		// Auto-resolve from member profile when not provided.
		if postalCode == "" {
			member, mErr := c.GetMember(ctx)
			if mErr != nil {
				return errResult("No postal_code provided and could not fetch member address: " + mErr.Error()), nil
			}
			postalCode = member.Address.PostalCode
			if postalCode == "" {
				return errResult("No postal_code provided and member profile has no address on file."), nil
			}
		}

		stores, err := cachedStoreSearch(ctx, c, postalCode)
		if err != nil {
			return errResult(fmt.Sprintf("Store search failed for %s: %v", postalCode, err)), nil
		}
		if len(stores) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No AH stores found near %s.", postalCode)), nil
		}

		type storeEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Type       string `json:"type,omitempty"`
			Street     string `json:"street,omitempty"`
			City       string `json:"city,omitempty"`
			PostalCode string `json:"postal_code,omitempty"`
		}
		results := make([]storeEntry, 0, len(stores))
		for _, st := range stores {
			results = append(results, storeEntry{
				ID:         st.ID,
				Name:       st.Name,
				Type:       st.StoreType,
				Street:     strings.TrimSpace(fmt.Sprintf("%s %s", st.Address.Street, st.Address.HouseNumber)),
				City:       st.Address.City,
				PostalCode: st.Address.PostalCode,
			})
		}
		return jsonResult(results)
	}))
}

// cachedStoreSearch looks up stores near a postal code. Store locations barely
// change, so results are cached for CacheTTLStores.
func cachedStoreSearch(ctx context.Context, c *appie.Client, postalCode string) ([]appie.Store, error) {
	cacheKey := "stores:" + strings.ToUpper(strings.ReplaceAll(postalCode, " ", ""))
	if cached, ok := GlobalCache.Get(cacheKey); ok {
		var stores []appie.Store
		if json.Unmarshal(cached, &stores) == nil {
			return stores, nil
		}
	}

	var stores []appie.Store
	if err := withRetry(ctx, "ah_search_stores", func() error {
		var e error
		stores, e = c.SearchStores(ctx, postalCode)
		return e
	}); err != nil {
		return nil, err
	}

	if data, err := json.Marshal(stores); err == nil {
		GlobalCache.Set(cacheKey, data, CacheTTLStores)
	}
	return stores, nil
}
