package bgooglechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/42wim/matterbridge/bridge/config"
	"google.golang.org/api/chat/v1"
)

// receivePubSubMessages receives messages from Pub/Sub subscription
func (b *Bgooglechat) receivePubSubMessages() {
	b.Log.Info("Starting Pub/Sub message receiver")

	err := b.pubsubSub.Receive(b.ctx, func(ctx context.Context, msg *pubsub.Message) {
		b.Log.Debugf("Received Pub/Sub message: %s", string(msg.Data))

		// Process the event
		if err := b.processChatEvent(msg.Data); err != nil {
			b.Log.Errorf("Error processing Pub/Sub event: %v", err)
		}

		// Acknowledge the message
		msg.Ack()
	})

	if err != nil {
		b.Log.Errorf("Error receiving Pub/Sub messages: %v", err)
	}
}

// handleWebhookEvent handles incoming HTTP webhook requests
func (b *Bgooglechat) handleWebhookEvent(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.Log.Errorf("Error reading webhook body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	b.Log.Debugf("Received webhook event: %s", string(body))

	// Process the event
	if err := b.processChatEvent(body); err != nil {
		b.Log.Errorf("Error processing webhook event: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Respond with success
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// processChatEvent processes a Google Chat event (from Pub/Sub or webhook)
func (b *Bgooglechat) processChatEvent(data []byte) error {
	// Parse the event
	var event chat.DeprecatedEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	b.Log.Debugf("Processing event type: %s", event.Type)

	// Route event to appropriate handler
	switch event.Type {
	case "MESSAGE":
		return b.handleMessageEvent(&event)
	case "ADDED_TO_SPACE":
		return b.handleAddedToSpaceEvent(&event)
	case "REMOVED_FROM_SPACE":
		return b.handleRemovedFromSpaceEvent(&event)
	case "CARD_CLICKED":
		// Interactive card events - can be extended later
		b.Log.Debug("Card clicked event received (ignoring)")
		return nil
	default:
		b.Log.Debugf("Unhandled event type: %s", event.Type)
		return nil
	}
}

// handleMessageEvent processes MESSAGE events
func (b *Bgooglechat) handleMessageEvent(event *chat.DeprecatedEvent) error {
	message := event.Message
	if message == nil {
		return nil
	}

	// Skip messages from the bot itself
	if message.Sender != nil && message.Sender.Type == "BOT" {
		b.Log.Debug("Skipping bot message")
		return nil
	}

	// Extract space name
	spaceName := event.Space.Name
	channelName := strings.TrimPrefix(spaceName, "spaces/")

	// Extract user info
	username := "Unknown"
	userID := ""
	if message.Sender != nil {
		username = message.Sender.DisplayName
		userID = message.Sender.Name
	}

	// Create matterbridge message
	rmsg := &config.Message{
		Text:     message.Text,
		Channel:  channelName,
		Username: username,
		UserID:   userID,
		Account:  b.Account,
		Protocol: b.Protocol,
		ID:       extractMessageID(message.Name),
		Event:    config.EventUserTyping,
	}

	// Handle threaded messages
	if message.Thread != nil && message.Thread.Name != "" {
		rmsg.ParentID = message.Thread.Name
	}

	// Handle attachments
	if len(message.Attachment) > 0 {
		b.handleIncomingAttachments(message, rmsg)
	}

	// Handle message annotations (like @mentions)
	if len(message.Annotations) > 0 {
		b.handleAnnotations(message, rmsg)
	}

	// Send to matterbridge router
	b.Log.Debugf("<= Sending message to router: %#v", rmsg)
	b.Remote <- *rmsg

	return nil
}

// handleAddedToSpaceEvent processes ADDED_TO_SPACE events
func (b *Bgooglechat) handleAddedToSpaceEvent(event *chat.DeprecatedEvent) error {
	space := event.Space
	if space == nil {
		return nil
	}

	b.Log.Infof("Bot added to space: %s (%s)", space.DisplayName, space.Name)

	// Cache the space
	b.Lock()
	b.spaces[strings.TrimPrefix(space.Name, "spaces/")] = space
	b.Unlock()

	// Optionally send a join event
	if !b.GetBool("nosendjoinpart") {
		spaceName := strings.TrimPrefix(space.Name, "spaces/")
		rmsg := config.Message{
			Text:     "Bot joined the space",
			Channel:  spaceName,
			Username: "system",
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventJoinLeave,
		}
		b.Remote <- rmsg
	}

	return nil
}

// handleRemovedFromSpaceEvent processes REMOVED_FROM_SPACE events
func (b *Bgooglechat) handleRemovedFromSpaceEvent(event *chat.DeprecatedEvent) error {
	space := event.Space
	if space == nil {
		return nil
	}

	b.Log.Infof("Bot removed from space: %s (%s)", space.DisplayName, space.Name)

	// Remove from cache
	b.Lock()
	delete(b.spaces, strings.TrimPrefix(space.Name, "spaces/"))
	b.Unlock()

	// Optionally send a leave event
	if !b.GetBool("nosendjoinpart") {
		spaceName := strings.TrimPrefix(space.Name, "spaces/")
		rmsg := config.Message{
			Text:     "Bot left the space",
			Channel:  spaceName,
			Username: "system",
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventJoinLeave,
		}
		b.Remote <- rmsg
	}

	return nil
}

// handleIncomingAttachments processes attachments in incoming messages
func (b *Bgooglechat) handleIncomingAttachments(message *chat.Message, rmsg *config.Message) {
	for _, attachment := range message.Attachment {
		// Add attachment info to message text
		if attachment.AttachmentDataRef != nil {
			rmsg.Text += fmt.Sprintf("\n[Attachment: %s]", attachment.AttachmentDataRef.ResourceName)
		}

		// Could be extended to download and forward files
		// For now, just include the reference
	}
}

// handleAnnotations processes message annotations (like @mentions)
func (b *Bgooglechat) handleAnnotations(message *chat.Message, rmsg *config.Message) {
	for _, annotation := range message.Annotations {
		// Handle user mentions
		if annotation.Type == "USER_MENTION" && annotation.UserMention != nil {
			mentioned := annotation.UserMention.User.DisplayName
			// Replace the annotation in text with a more readable format
			// Google Chat uses <users/123> format in text
			if annotation.UserMention.User.Name != "" {
				rmsg.Text = strings.ReplaceAll(
					rmsg.Text,
					fmt.Sprintf("<users/%s>", annotation.UserMention.User.Name),
					fmt.Sprintf("@%s", mentioned),
				)
			}
		}
	}
}

// extractMessageID extracts the message ID from full message name
// Example: "spaces/AAAA/messages/BBBB.CCCC" -> "BBBB.CCCC"
func extractMessageID(messageName string) string {
	parts := strings.Split(messageName, "/")
	if len(parts) >= 4 {
		return parts[3]
	}
	return messageName
}
