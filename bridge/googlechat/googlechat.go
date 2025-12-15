package bgooglechat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/helper"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/chat/v1"
	"google.golang.org/api/option"

	"cloud.google.com/go/pubsub"
)

const (
	// Config keys
	credentialsFileConfig = "CredentialsFile"
	webhookBindConfig     = "WebhookBindAddress"
	pubsubProjectConfig   = "PubSubProject"
	pubsubTopicConfig     = "PubSubTopic"
	pubsubSubConfig       = "PubSubSubscription"
	messageLength         = 4096 // Google Chat message limit
)

type Bgooglechat struct {
	sync.RWMutex
	*bridge.Config

	// Google Chat API client
	chatService *chat.Service
	ctx         context.Context

	// Space info cache
	spaces map[string]*chat.Space
	users  map[string]*chat.User

	// Pub/Sub support
	pubsubClient *pubsub.Client
	pubsubSub    *pubsub.Subscription

	// Webhook support
	webhookServer *http.Server
}

// New creates a new Google Chat bridge instance
func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bgooglechat{
		Config: cfg,
		ctx:    context.Background(),
		spaces: make(map[string]*chat.Space),
		users:  make(map[string]*chat.User),
	}
	return b
}

// Connect establishes connection to Google Chat API
func (b *Bgooglechat) Connect() error {
	b.Log.Info("Connecting to Google Chat")

	// Validate configuration
	credentialsFile := b.GetString(credentialsFileConfig)
	if credentialsFile == "" {
		return errors.New("CredentialsFile is required for Google Chat bridge")
	}

	// Initialize Google Chat service with service account
	// Try parsing as JSON content first
	creds, err := google.CredentialsFromJSON(
		b.ctx,
		[]byte(credentialsFile),
		chat.ChatBotScope,
	)
	if err != nil {
		// If that fails, try reading from file path
		data, readErr := os.ReadFile(credentialsFile)
		if readErr != nil {
			return fmt.Errorf("failed to load credentials from file %s: %w", credentialsFile, readErr)
		}
		creds, err = google.CredentialsFromJSON(b.ctx, data, chat.ChatBotScope)
		if err != nil {
			return fmt.Errorf("failed to parse credentials: %w", err)
		}
	}

	b.chatService, err = chat.NewService(b.ctx, option.WithCredentials(creds))
	if err != nil {
		return fmt.Errorf("failed to create Chat service: %w", err)
	}

	b.Log.Info("Google Chat API service initialized")

	// Initialize message receiving mechanism
	if err := b.initializeReceiver(); err != nil {
		return fmt.Errorf("failed to initialize message receiver: %w", err)
	}

	return nil
}

// initializeReceiver sets up either Pub/Sub or HTTP webhook for receiving messages
func (b *Bgooglechat) initializeReceiver() error {
	// Check if Pub/Sub is configured
	pubsubProject := b.GetString(pubsubProjectConfig)
	pubsubSub := b.GetString(pubsubSubConfig)

	if pubsubProject != "" && pubsubSub != "" {
		b.Log.Info("Initializing Pub/Sub receiver")
		return b.initPubSub(pubsubProject, pubsubSub)
	}

	// Check if HTTP webhook is configured
	webhookAddr := b.GetString(webhookBindConfig)
	if webhookAddr != "" {
		b.Log.Info("Initializing HTTP webhook receiver")
		return b.initWebhook(webhookAddr)
	}

	b.Log.Warn("No message receiver configured. Bot will only send messages, not receive them.")
	return nil
}

// initPubSub initializes Pub/Sub subscription for receiving messages
func (b *Bgooglechat) initPubSub(projectID, subscriptionID string) error {
	var err error
	b.pubsubClient, err = pubsub.NewClient(b.ctx, projectID)
	if err != nil {
		return fmt.Errorf("failed to create Pub/Sub client: %w", err)
	}

	b.pubsubSub = b.pubsubClient.Subscription(subscriptionID)

	// Start receiving messages in background
	go b.receivePubSubMessages()

	b.Log.Infof("Pub/Sub receiver started: project=%s, subscription=%s", projectID, subscriptionID)
	return nil
}

// initWebhook initializes HTTP webhook server for receiving messages
func (b *Bgooglechat) initWebhook(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", b.handleWebhookEvent)

	b.webhookServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start server in background
	go func() {
		b.Log.Infof("Starting webhook server on %s", addr)
		if err := b.webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.Log.Errorf("Webhook server error: %v", err)
		}
	}()

	return nil
}

// Disconnect closes the connection to Google Chat
func (b *Bgooglechat) Disconnect() error {
	b.Log.Info("Disconnecting from Google Chat")

	// Close Pub/Sub client if active
	if b.pubsubClient != nil {
		if err := b.pubsubClient.Close(); err != nil {
			b.Log.Errorf("Error closing Pub/Sub client: %v", err)
		}
	}

	// Shutdown webhook server if active
	if b.webhookServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.webhookServer.Shutdown(ctx); err != nil {
			b.Log.Errorf("Error shutting down webhook server: %v", err)
		}
	}

	return nil
}

// JoinChannel verifies that the bot is a member of the specified space
func (b *Bgooglechat) JoinChannel(channel config.ChannelInfo) error {
	spaceName := channel.Name

	// Ensure space name has proper format
	if !strings.HasPrefix(spaceName, "spaces/") {
		spaceName = "spaces/" + spaceName
	}

	b.Log.Infof("Verifying membership in space: %s", spaceName)

	// Get space info to verify access
	space, err := b.getSpace(spaceName)
	if err != nil {
		return fmt.Errorf("failed to verify space membership: %w", err)
	}

	// Cache space info
	b.Lock()
	b.spaces[channel.Name] = space
	b.Unlock()

	b.Log.Infof("Successfully verified access to space: %s", space.DisplayName)
	return nil
}

// Send sends a message to Google Chat
func (b *Bgooglechat) Send(msg config.Message) (string, error) {
	b.Log.Debugf("=> Sending message to Google Chat: %#v", msg)

	// Skip non-message events
	if msg.Event == config.EventJoinLeave || msg.Event == config.EventTopicChange {
		return "", nil
	}

	// Clip message to maximum length
	msg.Text = helper.ClipMessage(msg.Text, messageLength, b.GetString("MessageClipped"))

	// Get space name
	spaceName := msg.Channel
	if !strings.HasPrefix(spaceName, "spaces/") {
		spaceName = "spaces/" + spaceName
	}

	// Handle message deletion
	if msg.Event == config.EventMsgDelete {
		return b.deleteMessage(spaceName, msg.ID)
	}

	// Handle message edit
	if msg.ID != "" {
		return b.updateMessage(spaceName, msg.ID, &msg)
	}

	// Send new message
	return b.createMessage(spaceName, &msg)
}

// createMessage creates a new message in Google Chat
func (b *Bgooglechat) createMessage(spaceName string, msg *config.Message) (string, error) {
	chatMsg := &chat.Message{
		Text: b.formatMessageText(msg),
	}

	// Handle threaded replies
	if msg.ParentID != "" {
		chatMsg.Thread = &chat.Thread{
			Name: msg.ParentID,
		}
	}

	// Handle file attachments
	if len(msg.Extra) > 0 {
		attachments := b.handleAttachments(msg)
		if len(attachments) > 0 {
			chatMsg.Attachment = attachments
		}
	}

	// Create the message
	created, err := b.chatService.Spaces.Messages.Create(spaceName, chatMsg).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create message: %w", err)
	}

	return created.Name, nil
}

// updateMessage updates an existing message
func (b *Bgooglechat) updateMessage(spaceName, messageID string, msg *config.Message) (string, error) {
	messageName := messageID
	if !strings.Contains(messageID, "/messages/") {
		messageName = fmt.Sprintf("%s/messages/%s", spaceName, messageID)
	}

	chatMsg := &chat.Message{
		Text: b.formatMessageText(msg),
	}

	updated, err := b.chatService.Spaces.Messages.Update(messageName, chatMsg).Do()
	if err != nil {
		return "", fmt.Errorf("failed to update message: %w", err)
	}

	return updated.Name, nil
}

// deleteMessage deletes a message from Google Chat
func (b *Bgooglechat) deleteMessage(spaceName, messageID string) (string, error) {
	messageName := messageID
	if !strings.Contains(messageID, "/messages/") {
		messageName = fmt.Sprintf("%s/messages/%s", spaceName, messageID)
	}

	_, err := b.chatService.Spaces.Messages.Delete(messageName).Do()
	if err != nil {
		return "", fmt.Errorf("failed to delete message: %w", err)
	}

	return messageID, nil
}

// formatMessageText formats the message text with username prefix if configured
func (b *Bgooglechat) formatMessageText(msg *config.Message) string {
	text := msg.Text

	// Prepend username if configured
	if b.GetBool("PrefixMessagesWithNick") && msg.Username != "" {
		text = fmt.Sprintf("**%s**: %s", msg.Username, text)
	}

	// Handle action messages
	if msg.Event == config.EventUserAction {
		text = fmt.Sprintf("_%s_", text)
	}

	return text
}

// handleAttachments processes file attachments
func (b *Bgooglechat) handleAttachments(msg *config.Message) []*chat.Attachment {
	var attachments []*chat.Attachment

	// Handle file attachments
	if files, ok := msg.Extra["file"]; ok {
		for _, f := range files {
			fi, ok := f.(config.FileInfo)
			if !ok {
				continue
			}

			// For now, add file URL as text (actual upload would require more complex handling)
			if fi.URL != "" {
				attachment := &chat.Attachment{
					AttachmentDataRef: &chat.AttachmentDataRef{
						ResourceName: fi.Name,
					},
				}
				attachments = append(attachments, attachment)
			}
		}
	}

	return attachments
}

// getSpace retrieves and caches space information
func (b *Bgooglechat) getSpace(spaceName string) (*chat.Space, error) {
	b.RLock()
	if space, ok := b.spaces[spaceName]; ok {
		b.RUnlock()
		return space, nil
	}
	b.RUnlock()

	// Fetch from API
	space, err := b.chatService.Spaces.Get(spaceName).Do()
	if err != nil {
		return nil, err
	}

	// Cache it
	b.Lock()
	b.spaces[spaceName] = space
	b.Unlock()

	return space, nil
}
