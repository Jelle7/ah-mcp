package tools

import (
	"context"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterMemberTools registers member profile MCP tools.
func RegisterMemberTools(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_member_profile",
		mcp.WithTitleAnnotation("Albert Heijn: Member Profile"),
		mcp.WithDescription(
			"Get your Albert Heijn member profile. "+
				"Returns name, email, date_of_birth, and bonus_card_number (last 4 digits only).",
		),
	)
	s.AddTool(tool, withClient(deps, func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		member, err := c.GetMember(ctx)
		if err != nil {
			LogError("ah_get_member_profile", "failed: %v", err)
			return errResult("Failed to get member profile."), nil
		}

		// Mask bonus card: show only last 4 digits.
		bonusCard := member.BonusCardNumber
		if len(bonusCard) > 4 {
			bonusCard = "****" + bonusCard[len(bonusCard)-4:]
		}

		type profile struct {
			Name            string `json:"name"`
			Email           string `json:"email"`
			BonusCardNumber string `json:"bonus_card_number,omitempty"`
			DateOfBirth     string `json:"date_of_birth,omitempty"`
		}
		return jsonResult(profile{
			Name:            member.FirstName + " " + member.LastName,
			Email:           member.Email,
			BonusCardNumber: bonusCard,
			DateOfBirth:     member.DateOfBirth,
		})
	}))
}
