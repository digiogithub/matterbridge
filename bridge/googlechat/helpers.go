package bgooglechat

import (
	"fmt"
	"strings"

	"google.golang.org/api/chat/v1"
)

// getUserInfo retrieves user information from Google Chat API
func (b *Bgooglechat) getUserInfo(userName string) (*chat.User, error) {
	b.RLock()
	if user, ok := b.users[userName]; ok {
		b.RUnlock()
		return user, nil
	}
	b.RUnlock()

	// Note: Google Chat API doesn't have a direct user lookup endpoint
	// User info is typically obtained from message events
	// This is a placeholder for future expansion
	return nil, fmt.Errorf("user lookup not implemented")
}

// formatSpaceName ensures space name has proper format
func formatSpaceName(spaceName string) string {
	if !strings.HasPrefix(spaceName, "spaces/") {
		return "spaces/" + spaceName
	}
	return spaceName
}

// formatMessageName creates full message name from space and message ID
func formatMessageName(spaceName, messageID string) string {
	spaceName = formatSpaceName(spaceName)
	if strings.Contains(messageID, "/messages/") {
		return messageID
	}
	return fmt.Sprintf("%s/messages/%s", spaceName, messageID)
}

// parseSpaceDisplayName extracts a human-readable name from space
func parseSpaceDisplayName(space *chat.Space) string {
	if space.DisplayName != "" {
		return space.DisplayName
	}
	// Fallback to space type
	if space.Type == "ROOM" {
		return "Chat Room"
	} else if space.Type == "DM" {
		return "Direct Message"
	}
	return "Unknown Space"
}

// isValidSpaceName checks if a space name is valid
func isValidSpaceName(spaceName string) bool {
	return strings.HasPrefix(spaceName, "spaces/") && len(spaceName) > len("spaces/")
}
