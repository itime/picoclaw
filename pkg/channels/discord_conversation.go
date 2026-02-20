package channels

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// ConversationState represents the state of a multi-agent conversation
type ConversationState string

const (
	ConvStateIdle       ConversationState = "idle"
	ConvStateActive     ConversationState = "active"
	ConvStateProcessing ConversationState = "processing"
	ConvStateClosed     ConversationState = "closed"
)

// ConversationMessage represents a message in the conversation
type ConversationMessage struct {
	ID          string
	From        AgentType // Empty string means human
	To          []AgentType
	Content     string
	Timestamp   time.Time
	IsFromHuman bool
	Metadata    map[string]string
}

// Conversation tracks a multi-agent conversation session
type Conversation struct {
	ID           string
	ChannelID    string
	State        ConversationState
	PendingCount int // Number of in-flight messages awaiting response
	Messages     []ConversationMessage
	ActiveAgents map[AgentType]bool
	CreatedAt    time.Time
	LastActivity time.Time
	IdleTimeout  time.Duration
	mu           sync.RWMutex
}

// ConversationManager manages multiple conversations
type ConversationManager struct {
	conversations map[string]*Conversation // channelID -> Conversation
	idleTimeout   time.Duration
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewConversationManager creates a new conversation manager
func NewConversationManager(idleTimeout time.Duration) *ConversationManager {
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())

	cm := &ConversationManager{
		conversations: make(map[string]*Conversation),
		idleTimeout:   idleTimeout,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start idle conversation cleanup goroutine
	go cm.cleanupLoop()

	return cm
}

// GetOrCreateConversation gets existing or creates new conversation for a channel
func (cm *ConversationManager) GetOrCreateConversation(channelID string) *Conversation {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if conv, exists := cm.conversations[channelID]; exists {
		return conv
	}

	conv := &Conversation{
		ID:           fmt.Sprintf("conv_%s_%d", channelID, time.Now().UnixNano()),
		ChannelID:    channelID,
		State:        ConvStateIdle,
		PendingCount: 0,
		Messages:     make([]ConversationMessage, 0),
		ActiveAgents: make(map[AgentType]bool),
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		IdleTimeout:  cm.idleTimeout,
	}

	cm.conversations[channelID] = conv

	logger.InfoCF("conversation", "Created new conversation", map[string]any{
		"conv_id":    conv.ID,
		"channel_id": channelID,
	})

	return conv
}

// GetConversation gets a conversation by channel ID
func (cm *ConversationManager) GetConversation(channelID string) (*Conversation, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	conv, exists := cm.conversations[channelID]
	return conv, exists
}

// CloseConversation closes and removes a conversation
func (cm *ConversationManager) CloseConversation(channelID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if conv, exists := cm.conversations[channelID]; exists {
		conv.mu.Lock()
		conv.State = ConvStateClosed
		conv.mu.Unlock()

		delete(cm.conversations, channelID)

		logger.InfoCF("conversation", "Closed conversation", map[string]any{
			"conv_id":       conv.ID,
			"channel_id":    channelID,
			"message_count": len(conv.Messages),
		})
	}
}

// Stop stops the conversation manager
func (cm *ConversationManager) Stop() {
	cm.cancel()
}

// cleanupLoop periodically checks for idle conversations
func (cm *ConversationManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			return
		case <-ticker.C:
			cm.cleanupIdleConversations()
		}
	}
}

// cleanupIdleConversations closes conversations that have been idle too long
func (cm *ConversationManager) cleanupIdleConversations() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	toDelete := make([]string, 0)

	for channelID, conv := range cm.conversations {
		conv.mu.RLock()
		isIdle := conv.PendingCount == 0 && now.Sub(conv.LastActivity) > conv.IdleTimeout
		conv.mu.RUnlock()

		if isIdle {
			toDelete = append(toDelete, channelID)
		}
	}

	for _, channelID := range toDelete {
		conv := cm.conversations[channelID]
		conv.mu.Lock()
		conv.State = ConvStateClosed
		conv.mu.Unlock()

		delete(cm.conversations, channelID)

		logger.InfoCF("conversation", "Cleaned up idle conversation", map[string]any{
			"conv_id":    conv.ID,
			"channel_id": channelID,
		})
	}
}

// --- Conversation methods ---

// AddMessage adds a message to the conversation
func (c *Conversation) AddMessage(msg ConversationMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	msg.Timestamp = time.Now()
	c.Messages = append(c.Messages, msg)
	c.LastActivity = time.Now()

	if c.State == ConvStateIdle {
		c.State = ConvStateActive
	}

	// Track active agents
	if !msg.IsFromHuman && msg.From != "" {
		c.ActiveAgents[msg.From] = true
	}
	for _, to := range msg.To {
		c.ActiveAgents[to] = true
	}
}

// IncrementPending increments the pending message count
func (c *Conversation) IncrementPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PendingCount++
	c.State = ConvStateProcessing

	logger.DebugCF("conversation", "Pending incremented", map[string]any{
		"conv_id": c.ID,
		"pending": c.PendingCount,
	})
}

// DecrementPending decrements the pending message count
func (c *Conversation) DecrementPending() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.PendingCount > 0 {
		c.PendingCount--
	}

	if c.PendingCount == 0 {
		c.State = ConvStateActive
	}

	logger.DebugCF("conversation", "Pending decremented", map[string]any{
		"conv_id": c.ID,
		"pending": c.PendingCount,
	})
}

// GetPendingCount returns the current pending count
func (c *Conversation) GetPendingCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PendingCount
}

// IsIdle returns true if conversation has no pending messages
func (c *Conversation) IsIdle() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PendingCount == 0
}

// GetState returns the current conversation state
func (c *Conversation) GetState() ConversationState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.State
}

// GetActiveAgents returns list of agents that have participated
func (c *Conversation) GetActiveAgents() []AgentType {
	c.mu.RLock()
	defer c.mu.RUnlock()

	agents := make([]AgentType, 0, len(c.ActiveAgents))
	for agent := range c.ActiveAgents {
		agents = append(agents, agent)
	}
	return agents
}

// GetRecentMessages returns the N most recent messages
func (c *Conversation) GetRecentMessages(n int) []ConversationMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if n <= 0 || len(c.Messages) == 0 {
		return nil
	}

	start := len(c.Messages) - n
	if start < 0 {
		start = 0
	}

	result := make([]ConversationMessage, len(c.Messages)-start)
	copy(result, c.Messages[start:])
	return result
}

// BuildContextForAgent builds conversation context for a specific agent
func (c *Conversation) BuildContextForAgent(agent AgentType, maxMessages int) string {
	messages := c.GetRecentMessages(maxMessages)
	if len(messages) == 0 {
		return ""
	}

	var context string
	for _, msg := range messages {
		var sender string
		if msg.IsFromHuman {
			sender = "Human"
		} else if msg.From != "" {
			sender = string(msg.From)
		} else {
			sender = "System"
		}

		context += fmt.Sprintf("[%s]: %s\n", sender, msg.Content)
	}

	return context
}
