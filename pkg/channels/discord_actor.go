package channels

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

type ActorMessage struct {
	ID        string
	From      AgentType
	To        AgentType
	Content   string
	ChannelID string
	ReplyTo   string
	Timestamp time.Time
	Metadata  map[string]string
}

type ActorMailbox struct {
	agent    AgentType
	messages chan ActorMessage
	capacity int
	ctx      context.Context
	cancel   context.CancelFunc
	handler  ActorMessageHandler
	mu       sync.RWMutex
}

type ActorMessageHandler func(msg ActorMessage) error

func NewActorMailbox(agent AgentType, capacity int) *ActorMailbox {
	if capacity <= 0 {
		capacity = 100
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ActorMailbox{
		agent:    agent,
		messages: make(chan ActorMessage, capacity),
		capacity: capacity,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (m *ActorMailbox) SetHandler(handler ActorMessageHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = handler
}

func (m *ActorMailbox) Start() {
	go m.processLoop()
	logger.InfoCF("actor", "Mailbox started", map[string]any{
		"agent":    string(m.agent),
		"capacity": m.capacity,
	})
}

func (m *ActorMailbox) Stop() {
	m.cancel()
	close(m.messages)
	logger.InfoCF("actor", "Mailbox stopped", map[string]any{
		"agent": string(m.agent),
	})
}

func (m *ActorMailbox) Send(msg ActorMessage) error {
	select {
	case m.messages <- msg:
		logger.DebugCF("actor", "Message queued", map[string]any{
			"agent":   string(m.agent),
			"from":    string(msg.From),
			"msg_id":  msg.ID,
			"pending": len(m.messages),
		})
		return nil
	default:
		return fmt.Errorf("mailbox full for agent %s", m.agent)
	}
}

func (m *ActorMailbox) SendWithTimeout(msg ActorMessage, timeout time.Duration) error {
	select {
	case m.messages <- msg:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout sending to agent %s", m.agent)
	}
}

func (m *ActorMailbox) processLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case msg, ok := <-m.messages:
			if !ok {
				return
			}
			m.processMessage(msg)
		}
	}
}

func (m *ActorMailbox) processMessage(msg ActorMessage) {
	m.mu.RLock()
	handler := m.handler
	m.mu.RUnlock()

	if handler == nil {
		logger.WarnCF("actor", "No handler set for mailbox", map[string]any{
			"agent": string(m.agent),
		})
		return
	}

	if err := handler(msg); err != nil {
		logger.ErrorCF("actor", "Failed to process message", map[string]any{
			"agent":  string(m.agent),
			"msg_id": msg.ID,
			"error":  err.Error(),
		})
	}
}

func (m *ActorMailbox) QueueSize() int {
	return len(m.messages)
}

func (m *ActorMailbox) Agent() AgentType {
	return m.agent
}

type ActorSystem struct {
	mailboxes map[AgentType]*ActorMailbox
	router    *MessageRouter
	mu        sync.RWMutex
}

func NewActorSystem() *ActorSystem {
	return &ActorSystem{
		mailboxes: make(map[AgentType]*ActorMailbox),
	}
}

func (s *ActorSystem) RegisterAgent(agent AgentType, capacity int) *ActorMailbox {
	s.mu.Lock()
	defer s.mu.Unlock()

	mailbox := NewActorMailbox(agent, capacity)
	s.mailboxes[agent] = mailbox

	logger.InfoCF("actor", "Agent registered", map[string]any{
		"agent":    string(agent),
		"capacity": capacity,
	})

	return mailbox
}

func (s *ActorSystem) GetMailbox(agent AgentType) (*ActorMailbox, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mailbox, ok := s.mailboxes[agent]
	return mailbox, ok
}

func (s *ActorSystem) SetRouter(router *MessageRouter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.router = router
}

func (s *ActorSystem) Route(msg ActorMessage) error {
	s.mu.RLock()
	mailbox, ok := s.mailboxes[msg.To]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not registered", msg.To)
	}

	return mailbox.Send(msg)
}

func (s *ActorSystem) Broadcast(msg ActorMessage, agents []AgentType) []error {
	var errors []error

	for _, agent := range agents {
		targetMsg := msg
		targetMsg.To = agent

		if err := s.Route(targetMsg); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (s *ActorSystem) StartAll() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, mailbox := range s.mailboxes {
		mailbox.Start()
	}

	logger.InfoCF("actor", "All mailboxes started", map[string]any{
		"count": len(s.mailboxes),
	})
}

func (s *ActorSystem) StopAll() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, mailbox := range s.mailboxes {
		mailbox.Stop()
	}

	logger.InfoC("actor", "All mailboxes stopped")
}

func (s *ActorSystem) GetStats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]any)
	for agent, mailbox := range s.mailboxes {
		stats[string(agent)] = map[string]any{
			"queue_size": mailbox.QueueSize(),
			"capacity":   mailbox.capacity,
		}
	}

	return stats
}

type MessageRouter struct {
	actorSystem *ActorSystem
	convManager *ConversationManager
	discord     *MultiAgentDiscordChannel
	mu          sync.RWMutex
}

func NewMessageRouter(actorSystem *ActorSystem, convManager *ConversationManager) *MessageRouter {
	return &MessageRouter{
		actorSystem: actorSystem,
		convManager: convManager,
	}
}

func (r *MessageRouter) SetDiscordChannel(discord *MultiAgentDiscordChannel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discord = discord
}

func (r *MessageRouter) RouteToAgent(from AgentType, to AgentType, content, channelID string) error {
	msg := ActorMessage{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		From:      from,
		To:        to,
		Content:   content,
		ChannelID: channelID,
		Timestamp: time.Now(),
	}

	conv := r.convManager.GetOrCreateConversation(channelID)
	conv.IncrementPending()

	if err := r.actorSystem.Route(msg); err != nil {
		conv.DecrementPending()
		return err
	}

	conv.AddMessage(ConversationMessage{
		ID:          msg.ID,
		From:        from,
		To:          []AgentType{to},
		Content:     content,
		IsFromHuman: false,
	})

	return nil
}

func (r *MessageRouter) RouteToMultiple(from AgentType, targets []AgentType, content, channelID string) []error {
	var errors []error

	for _, to := range targets {
		if err := r.RouteToAgent(from, to, content, channelID); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (r *MessageRouter) RouteFromHuman(targets []AgentType, content, channelID, senderID string) []error {
	var errors []error

	conv := r.convManager.GetOrCreateConversation(channelID)

	conv.AddMessage(ConversationMessage{
		ID:          fmt.Sprintf("human_%d", time.Now().UnixNano()),
		From:        "",
		To:          targets,
		Content:     content,
		IsFromHuman: true,
		Metadata:    map[string]string{"sender_id": senderID},
	})

	if len(targets) == 0 {
		targets = []AgentType{AgentMaster}
	}

	for _, to := range targets {
		msg := ActorMessage{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			From:      "",
			To:        to,
			Content:   content,
			ChannelID: channelID,
			Timestamp: time.Now(),
			Metadata:  map[string]string{"sender_id": senderID, "is_human": "true"},
		}

		conv.IncrementPending()

		if err := r.actorSystem.Route(msg); err != nil {
			conv.DecrementPending()
			errors = append(errors, err)
		}
	}

	return errors
}

func (r *MessageRouter) HandleAgentResponse(from AgentType, content, channelID string) {
	conv, exists := r.convManager.GetConversation(channelID)
	if exists {
		conv.DecrementPending()
		conv.AddMessage(ConversationMessage{
			ID:          fmt.Sprintf("resp_%d", time.Now().UnixNano()),
			From:        from,
			To:          nil,
			Content:     content,
			IsFromHuman: false,
		})
	}
}
