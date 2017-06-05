package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/jinzhu/configor"
	"github.com/thoj/go-ircevent"
)

type Config struct {
	IRC struct {
		Server  string `default:"irc.freenode.net:6667"`
		UseTLS  bool   `default:false`
		Pass    string `default:""`
		Nick    string `required:"true"`
		Channel string `required:"true"`
	}
	Gitter struct {
		Server  string `default:"irc.gitter.im:6697"`
		Pass    string `required:"true"`
		Nick    string `required:"true"`
		Channel string `required:"true"`
	}
	Telegram struct {
		Token   string `required:"true"`
		Admins  string `required:"true"`
		GroupId string `default:"0"`
	}
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func goGitterIrcTelegram(conf Config) {
	//IRC init
	ircCon := irc.IRC(conf.IRC.Nick, conf.IRC.Nick)
	ircCon.UseTLS = conf.IRC.UseTLS
	ircCon.Password = conf.IRC.Pass

	//Gitter init
	gitterCon := irc.IRC(conf.Gitter.Nick, conf.Gitter.Nick)
	gitterCon.UseTLS = true
	gitterCon.Password = conf.Gitter.Pass

	//Telegram init
	bot, err := tgbotapi.NewBotAPI(conf.Telegram.Token)
	if err != nil {
		fmt.Printf("[Telegram] Error in NewBotAPI: %v...\n", err)
		return
	}
	fmt.Printf("[Telegram] Authorized on account %s\n", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		fmt.Printf("[Telegram] Error in GetUpdatesChan: %v...\n", err)
		return
	}
	groupId, err := strconv.ParseInt(conf.Telegram.GroupId, 10, 64)
	if err != nil {
		fmt.Printf("[Telegram] Error parsing GroupId: %v...\n", err)
		groupId = 0
	}
	fmt.Printf("[Telegram] GroupId: %v\n", groupId)

	//IRC loop
	if err := ircCon.Connect(conf.IRC.Server); err != nil {
		fmt.Printf("[IRC] Failed to connect to %v: %v...\n", conf.IRC.Server, err)
		return
	}
	ircCon.AddCallback("001", func(e *irc.Event) {
		ircCon.Join(conf.IRC.Channel)
	})
	ircCon.AddCallback("JOIN", func(e *irc.Event) {
		//IRC welcome message
		fmt.Printf("[IRC] Joined channel %v\n", conf.IRC.Channel)
		//ignore when other people join
		ircCon.ClearCallback("JOIN")
	})
	ircCon.AddCallback("PRIVMSG", func(e *irc.Event) {
		// strip mIRC color codes
                re := regexp.MustCompile("\x1f|\x02|\x03(?:\\d{1,2}(?:,\\d{1,2})?)?")
                msg := re.ReplaceAllString(e.Message(), "")
		//construct/log message
		ircMsg := fmt.Sprintf("<%v> %v", e.Nick, msg)
		fmt.Printf("[IRC] %v\n", ircMsg)
		//send to Gitter
		gitterCon.Privmsg(conf.Gitter.Channel, ircMsg)
		//send to Telegram
		if groupId != 0 {
			bot.Send(tgbotapi.NewMessage(groupId, ircMsg))
		}
	})
	go ircCon.Loop()

	//Gitter loop
	if err := gitterCon.Connect(conf.Gitter.Server); err != nil {
		fmt.Printf("[Gitter] Failed to connect to %v: %v...\n", conf.Gitter.Server, err)
		return
	}
	gitterCon.AddCallback("001", func(e *irc.Event) {
		gitterCon.Join(conf.Gitter.Channel)
	})
	gitterCon.AddCallback("JOIN", func(e *irc.Event) {
		//Gitter welcome message
		fmt.Printf("[Gitter] Joined channel %v\n", conf.Gitter.Channel)
		//ignore when other people join
		gitterCon.ClearCallback("JOIN")
	})
	gitterCon.AddCallback("PRIVMSG", func(e *irc.Event) {
		//construct message
		var gitterMsg string
		if e.Nick == "gitter" { //status messages
			gitterMsg = e.Message()
			match, _ := regexp.MatchString("\\[Github\\].+(opened|closed)", gitterMsg) //whitelist
			if !match {
				fmt.Printf("[Gitter Status] %v", gitterMsg)
				return
			}
		} else { //normal messages
			gitterMsg = fmt.Sprintf("<%v> %v", e.Nick, e.Message())
		}
		//log message
		fmt.Printf("[Gitter] %v\n", gitterMsg)
		//send to IRC
		ircCon.Privmsg(conf.IRC.Channel, gitterMsg)
		//send to Telegram
		if groupId != 0 {
			bot.Send(tgbotapi.NewMessage(groupId, gitterMsg))
		}
	})
	go gitterCon.Loop()

	//Telegram loop
	for update := range updates {
		//copy variables
		message := update.Message
		if message == nil {
			fmt.Printf("[Telegram] message == nil\n%v\n", update)
			continue
		}
		chat := message.Chat
		if chat == nil {
			fmt.Printf("[Telegram] chat == nil\n%v\n", update)
			continue
		}
		name := message.From.UserName
		if len(name) == 0 {
			name = message.From.FirstName
		}
		if len(message.Text) == 0 {
			continue
		}
		//construct/log message
		telegramMsg := fmt.Sprintf("<%s> %s", name, message.Text)
		fmt.Printf("[Telegram] %s\n", telegramMsg)
		//check for admin commands
		if stringInSlice(message.From.UserName, strings.Split(conf.Telegram.Admins, " ")) && strings.HasPrefix(message.Text, "/") {
			if message.Text == "/start" && (chat.IsGroup() || chat.IsSuperGroup()) {
				groupId = chat.ID
			} else if message.Text == "/status" {
				bot.Send(tgbotapi.NewMessage(int64(message.From.ID), fmt.Sprintf("groupId: %v, IRC: %v, Gitter: %v", groupId, ircCon.Connected(), gitterCon.Connected())))
			}
		} else if len(telegramMsg) > 0 {
			if groupId != 0 {
				//forward message to group
				if groupId != chat.ID {
					bot.Send(tgbotapi.NewMessage(groupId, telegramMsg))
				}
				//send to IRC
				ircCon.Privmsg(conf.IRC.Channel, telegramMsg)
				//send to Gitter
				gitterCon.Privmsg(conf.Gitter.Channel, telegramMsg)
			} else {
				fmt.Println("[Telegam] Use /start to start the bot...")
			}
		}
	}
}

func main() {
	fmt.Println("Gitter/IRC Sync Bot, written in Go by mrexodia")
	var conf Config
	if err := configor.Load(&conf, "config.json"); err != nil {
		fmt.Printf("Error loading config: %v...\n", err)
		return
	}
	goGitterIrcTelegram(conf)
}
