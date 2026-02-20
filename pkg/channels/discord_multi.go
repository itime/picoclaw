package channels

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	multiAgentSendTimeout = 10 * time.Second
)

// AgentType represents the type of agent in multi-agent discord
type AgentType string

const (
	AgentMaster AgentType = "master"
	AgentDev    AgentType = "dev"
	AgentQA     AgentType = "qa"
	AgentPM     AgentType = "pm"
	AgentOps    AgentType = "ops"
)

// AllAgentTypes lists all supported agent types
var AllAgentTypes = []AgentType{AgentMaster, AgentDev, AgentQA, AgentPM, AgentOps}

// AgentBot represents a single agent's Discord bot
type AgentBot struct {
	agentType AgentType
	session   *discordgo.Session
	botUserID string
	mu        sync.RWMutex
}

type MultiAgentDiscordChannel struct {
	config         config.MultiAgentDiscordConfig
	bus            *bus.MessageBus
	gatewaySession *discordgo.Session
	agentBots      map[AgentType]*AgentBot
	mainChannelID  string
	running        bool
	ctx            context.Context
	mu             sync.RWMutex
	mentionRegex   *regexp.Regexp
	actorSystem    *ActorSystem
	convManager    *ConversationManager
	router         *MessageRouter
}

func NewMultiAgentDiscordChannel(cfg config.MultiAgentDiscordConfig, msgBus *bus.MessageBus) (*MultiAgentDiscordChannel, error) {
	if cfg.GatewayToken == "" {
		return nil, fmt.Errorf("gateway token is required for multi-agent discord")
	}

	gatewaySession, err := discordgo.New("Bot " + cfg.GatewayToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create gateway discord session: %w", err)
	}

	actorSystem := NewActorSystem()
	convManager := NewConversationManager(5 * time.Minute)
	router := NewMessageRouter(actorSystem, convManager)

	channel := &MultiAgentDiscordChannel{
		config:         cfg,
		bus:            msgBus,
		gatewaySession: gatewaySession,
		agentBots:      make(map[AgentType]*AgentBot),
		running:        false,
		mentionRegex:   regexp.MustCompile(`(?i)@(master|dev|qa|pm|ops)\b`),
		actorSystem:    actorSystem,
		convManager:    convManager,
		router:         router,
	}

	router.SetDiscordChannel(channel)

	// Create agent bot sessions
	agentTokens := map[AgentType]string{
		AgentMaster: cfg.AgentTokens.Master,
		AgentDev:    cfg.AgentTokens.Dev,
		AgentQA:     cfg.AgentTokens.QA,
		AgentPM:     cfg.AgentTokens.PM,
		AgentOps:    cfg.AgentTokens.Ops,
	}

	for agentType, token := range agentTokens {
		if token == "" {
			logger.WarnCF("discord_multi", "No token configured for agent", map[string]any{
				"agent": string(agentType),
			})
			continue
		}

		session, err := discordgo.New("Bot " + token)
		if err != nil {
			logger.ErrorCF("discord_multi", "Failed to create session for agent", map[string]any{
				"agent": string(agentType),
				"error": err.Error(),
			})
			continue
		}

		channel.agentBots[agentType] = &AgentBot{
			agentType: agentType,
			session:   session,
		}

		logger.InfoCF("discord_multi", "Created bot session for agent", map[string]any{
			"agent": string(agentType),
		})
	}

	return channel, nil
}

// Name returns the channel name
func (c *MultiAgentDiscordChannel) Name() string {
	return "discord_multi"
}

// IsRunning returns whether the channel is running
func (c *MultiAgentDiscordChannel) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// IsAllowed always returns true for multi-agent discord (access control via guild membership)
func (c *MultiAgentDiscordChannel) IsAllowed(senderID string) bool {
	return true
}

func (c *MultiAgentDiscordChannel) Start(ctx context.Context) error {
	logger.InfoC("discord_multi", "Starting multi-agent Discord bots")

	c.ctx = ctx

	c.gatewaySession.AddHandler(c.handleGatewayMessage)
	if err := c.gatewaySession.Open(); err != nil {
		return fmt.Errorf("failed to open gateway discord session: %w", err)
	}

	gatewayUser, err := c.gatewaySession.User("@me")
	if err != nil {
		return fmt.Errorf("failed to get gateway bot user: %w", err)
	}
	logger.InfoCF("discord_multi", "Gateway bot connected", map[string]any{
		"username": gatewayUser.Username,
		"user_id":  gatewayUser.ID,
	})

	if err := c.findMainChannel(); err != nil {
		logger.WarnCF("discord_multi", "Failed to find main channel", map[string]any{
			"channel_name": c.config.Channels.Main,
			"error":        err.Error(),
		})
	}

	for agentType, bot := range c.agentBots {
		if err := bot.session.Open(); err != nil {
			logger.ErrorCF("discord_multi", "Failed to open agent session", map[string]any{
				"agent": string(agentType),
				"error": err.Error(),
			})
			continue
		}

		botUser, err := bot.session.User("@me")
		if err != nil {
			logger.ErrorCF("discord_multi", "Failed to get agent bot user", map[string]any{
				"agent": string(agentType),
				"error": err.Error(),
			})
			continue
		}

		bot.mu.Lock()
		bot.botUserID = botUser.ID
		bot.mu.Unlock()

		c.actorSystem.RegisterAgent(agentType, 100)

		logger.InfoCF("discord_multi", "Agent bot connected", map[string]any{
			"agent":    string(agentType),
			"username": botUser.Username,
			"user_id":  botUser.ID,
		})
	}

	c.actorSystem.StartAll()

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	return nil
}

// findMainChannel finds the main channel ID by name in the configured guild
func (c *MultiAgentDiscordChannel) findMainChannel() error {
	if c.config.GuildID == "" {
		return fmt.Errorf("guild_id is not configured")
	}

	channels, err := c.gatewaySession.GuildChannels(c.config.GuildID)
	if err != nil {
		return fmt.Errorf("failed to get guild channels: %w", err)
	}

	for _, ch := range channels {
		if ch.Name == c.config.Channels.Main {
			c.mainChannelID = ch.ID
			logger.InfoCF("discord_multi", "Found main channel", map[string]any{
				"channel_name": ch.Name,
				"channel_id":   ch.ID,
			})
			return nil
		}
	}

	return fmt.Errorf("channel %q not found in guild", c.config.Channels.Main)
}

func (c *MultiAgentDiscordChannel) Stop(ctx context.Context) error {
	logger.InfoC("discord_multi", "Stopping multi-agent Discord bots")

	c.mu.Lock()
	c.running = false
	c.mu.Unlock()

	c.actorSystem.StopAll()
	c.convManager.Stop()

	for agentType, bot := range c.agentBots {
		if err := bot.session.Close(); err != nil {
			logger.ErrorCF("discord_multi", "Failed to close agent session", map[string]any{
				"agent": string(agentType),
				"error": err.Error(),
			})
		}
	}

	if err := c.gatewaySession.Close(); err != nil {
		return fmt.Errorf("failed to close gateway discord session: %w", err)
	}

	return nil
}

// Send sends a message through the default agent (Master)
func (c *MultiAgentDiscordChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("multi-agent discord not running")
	}

	channelID := msg.ChatID
	if channelID == "" {
		channelID = c.mainChannelID
	}
	if channelID == "" {
		return fmt.Errorf("channel ID is empty and main channel not found")
	}

	return c.SendAsAgent(ctx, AgentMaster, channelID, msg.Content)
}

// SendAsAgent sends a message as a specific agent
func (c *MultiAgentDiscordChannel) SendAsAgent(ctx context.Context, agentType AgentType, channelID, content string) error {
	bot, ok := c.agentBots[agentType]
	if !ok {
		logger.WarnCF("discord_multi", "Agent bot not available, using gateway", map[string]any{
			"agent": string(agentType),
		})
		return c.sendViaGateway(ctx, channelID, content)
	}

	chunks := splitMessage(content, 1500)
	for _, chunk := range chunks {
		if err := c.sendChunkAsAgent(ctx, bot, channelID, chunk); err != nil {
			return err
		}
	}

	logger.DebugCF("discord_multi", "Message sent", map[string]any{
		"agent":      string(agentType),
		"channel_id": channelID,
		"length":     len(content),
	})

	return nil
}

func (c *MultiAgentDiscordChannel) sendChunkAsAgent(ctx context.Context, bot *AgentBot, channelID, content string) error {
	sendCtx, cancel := context.WithTimeout(ctx, multiAgentSendTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := bot.session.ChannelMessageSend(channelID, content)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to send message as agent %s: %w", bot.agentType, err)
		}
		return nil
	case <-sendCtx.Done():
		return fmt.Errorf("send message timeout for agent %s: %w", bot.agentType, sendCtx.Err())
	}
}

func (c *MultiAgentDiscordChannel) sendViaGateway(ctx context.Context, channelID, content string) error {
	sendCtx, cancel := context.WithTimeout(ctx, multiAgentSendTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.gatewaySession.ChannelMessageSend(channelID, content)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to send message via gateway: %w", err)
		}
		return nil
	case <-sendCtx.Done():
		return fmt.Errorf("send message timeout via gateway: %w", sendCtx.Err())
	}
}

func (c *MultiAgentDiscordChannel) handleGatewayMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil {
		return
	}

	if c.isOurBot(m.Author.ID) {
		return
	}

	if c.mainChannelID != "" && m.ChannelID != c.mainChannelID {
		return
	}

	if err := s.ChannelTyping(m.ChannelID); err != nil {
		logger.DebugCF("discord_multi", "Failed to send typing indicator", map[string]any{
			"error": err.Error(),
		})
	}

	targetAgents := c.parseMentions(m.Content)
	content := c.cleanMentions(m.Content)

	senderID := m.Author.ID
	senderName := m.Author.Username
	if m.Author.Discriminator != "" && m.Author.Discriminator != "0" {
		senderName += "#" + m.Author.Discriminator
	}

	logger.InfoCF("discord_multi", "Received message", map[string]any{
		"sender":        senderName,
		"target_agents": targetAgents,
		"preview":       truncateForLog(content, 50),
	})

	if errs := c.router.RouteFromHuman(targetAgents, content, m.ChannelID, senderID); len(errs) > 0 {
		for _, err := range errs {
			logger.ErrorCF("discord_multi", "Failed to route message", map[string]any{
				"error": err.Error(),
			})
		}
	}

	metadata := map[string]string{
		"message_id":    m.ID,
		"user_id":       senderID,
		"username":      m.Author.Username,
		"display_name":  senderName,
		"guild_id":      m.GuildID,
		"channel_id":    m.ChannelID,
		"target_agents": strings.Join(agentTypesToStrings(targetAgents), ","),
	}

	sessionKey := fmt.Sprintf("discord_multi:%s", m.ChannelID)

	msg := bus.InboundMessage{
		Channel:    "discord_multi",
		SenderID:   senderID,
		ChatID:     m.ChannelID,
		Content:    content,
		Media:      nil,
		SessionKey: sessionKey,
		Metadata:   metadata,
	}

	c.bus.PublishInbound(msg)
}

// parseMentions extracts agent types from @mentions in the message
func (c *MultiAgentDiscordChannel) parseMentions(content string) []AgentType {
	matches := c.mentionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[AgentType]bool)
	var agents []AgentType

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		agentStr := strings.ToLower(match[1])
		agentType := AgentType(agentStr)

		if !seen[agentType] {
			seen[agentType] = true
			agents = append(agents, agentType)
		}
	}

	return agents
}

// cleanMentions removes @agent mentions from the message content
func (c *MultiAgentDiscordChannel) cleanMentions(content string) string {
	cleaned := c.mentionRegex.ReplaceAllString(content, "")
	return strings.TrimSpace(cleaned)
}

// isOurBot checks if the given user ID belongs to one of our bots
func (c *MultiAgentDiscordChannel) isOurBot(userID string) bool {
	// Check gateway bot
	if c.gatewaySession.State != nil && c.gatewaySession.State.User != nil {
		if c.gatewaySession.State.User.ID == userID {
			return true
		}
	}

	// Check agent bots
	for _, bot := range c.agentBots {
		bot.mu.RLock()
		botID := bot.botUserID
		bot.mu.RUnlock()

		if botID == userID {
			return true
		}
	}

	return false
}

// getAgentFromMetadata extracts the agent type from message metadata
func (c *MultiAgentDiscordChannel) getAgentFromMetadata(metadata map[string]string) AgentType {
	if metadata == nil {
		return AgentMaster
	}

	if agentStr, ok := metadata["agent"]; ok {
		return AgentType(strings.ToLower(agentStr))
	}

	return AgentMaster
}

func agentTypesToStrings(agents []AgentType) []string {
	result := make([]string, len(agents))
	for i, a := range agents {
		result[i] = string(a)
	}
	return result
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetAgentBot returns the bot for a specific agent type
func (c *MultiAgentDiscordChannel) GetAgentBot(agentType AgentType) (*AgentBot, bool) {
	bot, ok := c.agentBots[agentType]
	return bot, ok
}

// GetMainChannelID returns the main channel ID
func (c *MultiAgentDiscordChannel) GetMainChannelID() string {
	return c.mainChannelID
}

// SetTypingIndicator sends a typing indicator to the channel
func (c *MultiAgentDiscordChannel) SetTypingIndicator(channelID string) error {
	return c.gatewaySession.ChannelTyping(channelID)
}

func (c *MultiAgentDiscordChannel) BroadcastToAllAgents(ctx context.Context, channelID, content string) error {
	var lastErr error
	for agentType := range c.agentBots {
		if err := c.SendAsAgent(ctx, agentType, channelID, content); err != nil {
			lastErr = err
			logger.ErrorCF("discord_multi", "Failed to broadcast from agent", map[string]any{
				"agent": string(agentType),
				"error": err.Error(),
			})
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}

func (c *MultiAgentDiscordChannel) GetActorSystem() *ActorSystem {
	return c.actorSystem
}

func (c *MultiAgentDiscordChannel) GetConversationManager() *ConversationManager {
	return c.convManager
}

func (c *MultiAgentDiscordChannel) GetRouter() *MessageRouter {
	return c.router
}

func (c *MultiAgentDiscordChannel) SetAgentHandler(agent AgentType, handler ActorMessageHandler) {
	if mailbox, ok := c.actorSystem.GetMailbox(agent); ok {
		mailbox.SetHandler(handler)
	}
}
