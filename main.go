package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
	"github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v2"
	"github.com/z4x7k/iran-domains-tg-bot/db/migration"
	"github.com/z4x7k/iran-domains-tg-bot/ratelimit"
)

const (
	EnvKeyBotToken               = "BOT_TOKEN"
	EnvKeyPublishChatID          = "PUBLISH_CHAT_ID"
	EnvKeyBotHTTPProxyURL        = "BOT_HTTP_PROXY_URL"
	ParseModeMarkdownV1          = models.ParseMode("Markdown")
	CLIRunCommandName            = "run"
	CLIRunCommandDBFileNameFlag  = "db"
	RateLimiterMaxAttemptsPerDay = 200
)

var (
	AppVersion     = "0.0.0"
	AppCompileTime = "1991-11-22T00:11:22+00:00"
)

func main() {
	compileTime, err := time.Parse(time.RFC3339, AppCompileTime)
	if nil != err {
		panic(err)
	}

	log := zerolog.New(zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) { w.Out = os.Stderr; w.TimeFormat = time.RFC3339 })).With().Timestamp().Logger().Level(zerolog.TraceLevel)

	app := &cli.App{
		Name:           "iran-domains-tg-bot",
		Version:        AppVersion,
		Compiled:       compileTime,
		Suggest:        true,
		Usage:          "Iranian Domains Telegram Bot",
		DefaultCommand: CLIRunCommandName,
		Commands: []*cli.Command{
			{
				Name:   CLIRunCommandName,
				Usage:  "Start the bot server",
				Action: buildBot(log),
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     CLIRunCommandDBFileNameFlag,
						Usage:    "Database file name. Defaults to domains.db in the current working directory",
						Required: false,
					},
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("command failed")
	}
}

func buildBot(log zerolog.Logger) func(*cli.Context) error {
	return func(cliCtx *cli.Context) error {
		ctx, cancel := signal.NotifyContext(cliCtx.Context, os.Interrupt)
		defer cancel()

		if err := godotenv.Load(); nil != err {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("env: unexpected error while loading environment variables from .env file")
			}
			log.Warn().Msg(".env file not found")
		}

		tz, ok := os.LookupEnv("TZ")
		if !ok || tz != "UTC" {
			return errors.New("env: TZ environment variable must be set to UTC")
		}

		dbFileName := cliCtx.String(CLIRunCommandDBFileNameFlag)
		if dbFileName == "" {
			dbFileName = "domains.db"
		}

		db, err := sql.Open("sqlite3", dbFileName)
		if nil != err {
			return fmt.Errorf("db: failed to open database: %v", err)
		}
		defer func() {
			log.Info().Msg("closing database connection")
			if err := db.Close(); nil != err {
				log.Error().Err(err).Msg("failed to close database connection")
			}
		}()
		if err := db.PingContext(ctx); nil != err {
			return fmt.Errorf("db: failed to ping database connection: %v", err)
		}
		sqliteLibVersion, sqliteLibVersionNumber, _ := sqlite3.Version()
		log.Info().Str("lib_version", sqliteLibVersion).Int("lib_version_number", sqliteLibVersionNumber).Msg("successfully connected to sqlite database")
		if err := execPragmas(ctx, db); nil != err {
			return fmt.Errorf("db: unable to execute database pragmas: %v", err)
		}
		log.Info().Msg("successfully executed database pragmas")

		goose.SetLogger(goose.NopLogger())
		goose.SetTableName("migrations")
		goose.SetBaseFS(migration.FS)
		if err := goose.SetDialect("sqlite3"); nil != err {
			return fmt.Errorf("db: failed to set goose dialect to sqlite: %v", err)
		}
		if err := goose.Up(db, "scripts"); nil != err {
			return fmt.Errorf("db: failed to execute goose migrations: %v", err)
		}
		log.Info().Msg("executed database migrations")

		publishChatID, ok := os.LookupEnv(EnvKeyPublishChatID)
		if !ok {
			return fmt.Errorf("env: required environment variable '%s' is not set", EnvKeyPublishChatID)
		}

		rl := ratelimit.New(db, RateLimiterMaxAttemptsPerDay, time.Hour*24)

		handler := Handler{
			log:           log,
			publishChatID: publishChatID,
			db:            db,
			rateLimiter:   &rl,
		}

		httpTransport := http.Transport{IdleConnTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
		httpClient := http.Client{Timeout: time.Second * 35, Transport: &httpTransport}
		proxyURL, ok := os.LookupEnv(EnvKeyBotHTTPProxyURL)
		if ok && proxyURL != "" {
			httpProxyURL, err := url.Parse(proxyURL)
			if nil != err {
				return fmt.Errorf("proxy: failed to parse bot http proxy url: %v", err)
			}
			httpTransport.Proxy = http.ProxyURL(httpProxyURL)
		}

		opts := []bot.Option{
			bot.WithCheckInitTimeout(5 * time.Second),
			bot.WithHTTPClient(25*time.Second, &httpClient),
			bot.WithDefaultHandler(handler.handleMessage),
		}

		token, ok := os.LookupEnv(EnvKeyBotToken)
		if !ok {
			return fmt.Errorf("env: required environment variable '%s' is not set", EnvKeyBotToken)
		}

		b, err := bot.New(token, opts...)
		if nil != err {
			return fmt.Errorf("bot: failed to initialize bot instance: %v", err)
		}

		b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, handler.handleStartCommand)
		b.RegisterHandler(bot.HandlerTypeMessageText, "/info", bot.MatchTypeExact, handler.handleInfoCommand)
		b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, handler.handleHelpCommand)
		b.Start(ctx)

		return nil
	}
}

type Handler struct {
	log           zerolog.Logger
	publishChatID string
	db            *sql.DB
	rateLimiter   *ratelimit.RateLimiter
}

func extractDomainApexZone(msg string) (string, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(msg))
	if nil != err {
		return "", err
	}

	domain := parsedURL.Host
	if domain == "" {
		path := parsedURL.Path
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 1 {
			return "", fmt.Errorf("could not extract domain from path '%s'", path)
		}
		domain = parts[0]
	}

	partsCount := strings.Count(domain, ".")
	if partsCount > 5 {
		return "", fmt.Errorf("subdomains depth exceeded maximum limit in '%s'", domain)
	}
	if partsCount < 1 {
		return "", fmt.Errorf("could not find domain apex zone and tld parts in '%s'", domain)
	}
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("could not extract domain apex zone from '%s'", domain)
	}
	apex, tld := parts[partsCount-1], parts[partsCount]

	return apex + "." + tld, nil
}

func (h *Handler) handleMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	if shouldDiscard(update) {
		return
	}

	log := h.loggerFromUpdate(update)

	if canPass, err := h.rateLimiter.CanPass(ctx, update.Message.From.ID); nil != err {
		log.Error().Err(err).Msg("failed to check user rate limit")
	} else if !canPass {
		return
	}

	domain, err := extractDomainApexZone(update.Message.Text)
	if nil != err {
		log.
			Debug().
			Err(err).
			Msg("failed to extract domain from message text")
		return
	}
	log = log.With().Str("domain", domain).Logger()

	if err := insertDomain(ctx, h.db, domain); nil != err {
		if errors.Is(err, errDuplicateDomain) {
			return
		}
		log.Error().Err(err).Msg("failed to insert domain into database")
		h.informSupport(ctx, b, err)
		return
	}

	successMessageText := "`" + domain + "`"
	chatID := update.Message.Chat.ID
	replyMsg := bot.SendMessageParams{
		ChatID:           chatID,
		ReplyToMessageID: update.Message.ID,
		Text:             successMessageText,
		ParseMode:        ParseModeMarkdownV1,
	}
	if _, err := b.SendMessage(ctx, &replyMsg); nil != err {
		log.
			Error().
			Err(err).
			Dict("reply_message", zerolog.Dict().
				Int64("chat_id", chatID).
				Str("text", successMessageText),
			).
			Msg("failed to send success reply message to user chat")
		return
	}
}

func (h *Handler) informSupport(ctx context.Context, b *bot.Bot, err error) {
	chatID := h.publishChatID
	msg := bot.SendMessageParams{
		ChatID:    chatID,
		Text:      "🚨 An unexpected error occurred. Please check the logs...\n\n```\n" + err.Error() + "```",
		ParseMode: ParseModeMarkdownV1,
	}
	if _, sendErr := b.SendMessage(ctx, &msg); nil != sendErr {
		h.log.
			Error().
			Err(sendErr).
			AnErr("root_error", err).
			Dict("reply_message", zerolog.Dict().
				Str("chat_id", chatID),
			).
			Msg("failed to send message to support user chat")
		return
	}
}

func (h *Handler) handleStartCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	if shouldDiscard(update) {
		return
	}
	log := h.loggerFromUpdate(update)

	replyText := strings.Join(
		[]string{
			fmt.Sprintf("Compiled At: `%s`", bot.EscapeMarkdown(AppCompileTime)),
			fmt.Sprintf("Version: `%s`", bot.EscapeMarkdown(AppVersion)),
		},
		"\n",
	)
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      replyText,
		ParseMode: models.ParseModeMarkdown,
	}); nil != err {
		log.Error().Err(err).Str("reply_text", replyText).Msg("failed to send start command success reply message")
	}
}

//go:embed info.txt
var infoCommandReplyMessageText string

func (h *Handler) handleInfoCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	if shouldDiscard(update) {
		return
	}
	log := h.loggerFromUpdate(update)

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   infoCommandReplyMessageText,
	}); nil != err {
		log.Error().Err(err).Msg("failed to send info command success reply message")
	}
}

//go:embed help.txt
var helpCommandReplyMessageText string

func (h *Handler) handleHelpCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	if shouldDiscard(update) {
		return
	}
	log := h.loggerFromUpdate(update)

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      helpCommandReplyMessageText,
		ParseMode: ParseModeMarkdownV1,
	}); nil != err {
		log.Error().Err(err).Msg("failed to send help command success reply message")
	}
}
