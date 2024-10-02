package app

import (
	"apubot/internal/config"
	"apubot/internal/handler"
	"apubot/internal/infrastructure/database"
	"apubot/internal/infrastructure/repository"
	"apubot/internal/service"
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/patrickmn/go-cache"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type App struct {
	cfg       *config.Config
	db        *database.DB
	bot       *tgbotapi.BotAPI
	handlers  *handler.Handlers
	lastUsage *cache.Cache
}

func New(cfg *config.Config) *App {
	bot, err := tgbotapi.NewBotAPI(cfg.ApiKey)
	if err != nil {
		log.Fatalf("Error creating bot: %v", err)
	}

	bot.Debug = cfg.IsDebug

	db, err := database.New(cfg)
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}

	repos := repository.New(
		&repository.InitParams{
			Config: cfg,
			DB:     db,
		},
	)

	services := service.New(
		&service.InitParams{
			Config:       cfg,
			Repositories: repos,
		},
	)

	handlers := handler.New(
		&handler.InitParams{
			Config:   cfg,
			Bot:      bot,
			Services: services,
		},
	)

	lastUsage := cache.New(cfg.CommandCooldown, 5*time.Minute)

	return &App{
		cfg:       cfg,
		bot:       bot,
		db:        db,
		handlers:  handlers,
		lastUsage: lastUsage,
	}
}

func (a *App) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updatesChan := a.bot.GetUpdatesChan(u)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case update := <-updatesChan:
			a.handleUpdate(&update)
		case <-c:
			log.Println("Stopping bot...")

			a.bot.StopReceivingUpdates()
			_ = a.db.Close()

			log.Println("Bot gracefully stopped!")

			return
		}
	}
}

func (a *App) handleUpdate(update *tgbotapi.Update) {
	if lastTime, ok := a.lastUsage.Get(fmt.Sprint(update.Message.Chat.ID)); ok {
		waitTime := a.cfg.CommandCooldown - time.Since(lastTime.(time.Time))
		if waitTime > 0 {
			msgText := fmt.Sprintf("Command on cooldown for %.1f sec", waitTime.Seconds())
			go a.handlers.General.MessageResponse(update.Message.Chat.ID, msgText)

			return
		}
	}

	a.lastUsage.Set(fmt.Sprint(update.Message.Chat.ID), time.Now(), cache.DefaultExpiration)

	if update.Message == nil {
		return
	}

	if !update.Message.IsCommand() {
		msgText := "I can only handle listed commands in this chat!"
		go a.handlers.General.MessageResponse(update.Message.Chat.ID, msgText)

		return
	}

	switch update.Message.Command() {
	case "start":
		go a.handlers.General.StartResponse(update.Message.Chat.ID)
	case "peepo":
		ctx := context.Background()
		go a.handlers.Image.GetImage(ctx, update.Message)
	case "sub":
		ctx := context.Background()
		go a.handlers.Image.CreateSubscription(ctx, update.Message)
	case "unsub":
		ctx := context.Background()
		go a.handlers.Image.DeleteSubscription(ctx, update.Message)
	case "sub_info":
		ctx := context.Background()
		go a.handlers.Image.GetSubscription(ctx, update.Message)
	case "help":
		go a.handlers.General.HelpResponse(update.Message.Chat.ID)
	default:
		go a.handlers.General.MessageResponse(update.Message.Chat.ID, "Unknown command")
	}
}
