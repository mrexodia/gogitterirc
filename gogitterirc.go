package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/jinzhu/configor"
	"github.com/mattn/go-xmpp"
	"github.com/thoj/go-ircevent"
)

type Config struct {
	IRC struct {
		Server   string `default:"irc.freenode.net:6667"`
		UseTLS   bool   `default:"false"`
		Pass     string `default:""`
		Nick     string `required:"true"`
		Channel  string `required:"true"`
		Identify string `default:""`
	}
	Gitter struct {
		Server  string `default:"irc.gitter.im:6697"`
		Pass    string `required:"true"`
		Nick    string `required:"true"`
		Channel string `required:"true"`
	}
	Telegram struct {
		Token         string `required:"true"`
		Admins        string `required:"true"`
		GroupId       string `default:"0"`
		ImgurClientId string `default:""`
	}
	XMPP struct {
		Server string `required:"true"`
		Jid    string `required:"true"`
		Pass   string `required:"true"`
		Muc    string `required:"true"`
		Nick   string `required:"true"`
	}
}

type ImgurResponse struct {
	Data    ImageData `json:"data"`
	Status  int       `json:"status"`
	Success bool      `json:"success"`
}

type ImageData struct {
	Account_id int    `json:"account_id"`
	Animated   bool   `json:"animated"`
	Bandwidth  int    `json:"bandwidth"`
	DateTime   int    `json:"datetime"`
	Deletehash string `json:"deletehash"`
	Favorite   bool   `json:"favorite"`
	Height     int    `json:"height"`
	Id         string `json:"id"`
	In_gallery bool   `json:"in_gallery"`
	Is_ad      bool   `json:"is_ad"`
	Link       string `json:"link"`
	Name       string `json:"name"`
	Size       int    `json:"size"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Views      int    `json:"views"`
	Width      int    `json:"width"`
}

func imgurUploadImageByURL(clientID string, imageURL string) (string, error) {
	req, err := http.NewRequest("POST", "https://api.imgur.com/3/image", strings.NewReader(url.Values{"image": {imageURL}}.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", "Client-ID "+clientID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var imgurResponse ImgurResponse
	err = json.NewDecoder(res.Body).Decode(&imgurResponse)
	if err != nil {
		return "", err
	}
	if !imgurResponse.Success {
		return "", errors.New("imgur API returned negative response")
	}
	fmt.Println("Image Link: " + imgurResponse.Data.Link)
	fmt.Println("Deletion Link: http://imgur.com/delete/" + imgurResponse.Data.Deletehash)
	return imgurResponse.Data.Link, nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func ircPrivMsg(irc *irc.Connection, target string, author string, message string) {
	messages := strings.Split(strings.Replace(message, "\r", "", -1), "\n")
	for _, x := range messages {
		irc.Privmsg(target, fmt.Sprintf("<%v> %v", author, x))
	}
}

func gitterEscape(msg string) string {
	// [![asm.png](https://files.gitter.im/x64dbg/x64dbg/0I1c/thumb/asm.png)](https://files.gitter.im/x64dbg/x64dbg/0I1c/asm.png)
	r1 := regexp.MustCompile("^\\[!\\[[^\\]]+\\]\\(https?:\\/\\/files\\.gitter\\.im\\/[^\\/]+\\/[^\\/]+\\/[^\\/]+\\/thumb\\/[^\\)]+\\)\\]\\(([^\\)]+)\\)$")
	msg = r1.ReplaceAllString(msg, "$1")
	// [test.exe](https://files.gitter.im/x64dbg/x64dbg/ROVJ/test.exe)
	r2 := regexp.MustCompile("\\[[^\\]]+\\]\\((https:\\/\\/files\\.gitter\\.im\\/[^\\/]+\\/[^\\/]+\\/[^\\/]+\\/[^\\)]+)\\)$")
	msg = r2.ReplaceAllString(msg, "$1")
	return msg
}

func initXmppClient(conf Config) (*xmpp.Client, error) {
	xmppOptions := xmpp.Options{Host: conf.XMPP.Server,
		User:                         conf.XMPP.Jid,
		Password:                     conf.XMPP.Pass,
		InsecureAllowUnencryptedAuth: false,
		NoTLS:                        true,
		StartTLS:                     true,
		Debug:                        false,
		Status:                       "chat",
		StatusMessage:                "https://github.com/mrexodia/gogitterirc",
	}
	xmppClient, err := xmppOptions.NewClient()
	if err != nil {
		log.Printf("[XMPP] Error creating client: %v...\n", err)
		return nil, err
	}
	_, err = xmppClient.JoinMUCNoHistory(conf.XMPP.Muc, conf.XMPP.Nick)
	if err != nil {
		log.Printf("[XMPP] Error joining MUC: %v\n", err)
		return nil, err
	}
	return xmppClient, nil
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
		log.Printf("[Telegram] Error in NewBotAPI: %v...\n", err)
		return
	}
	log.Printf("[Telegram] Authorized on account %s\n", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Printf("[Telegram] Error in GetUpdatesChan: %v...\n", err)
		return
	}
	groupId, err := strconv.ParseInt(conf.Telegram.GroupId, 10, 64)
	if err != nil {
		log.Printf("[Telegram] Error parsing GroupId: %v...\n", err)
		groupId = 0
	}
	log.Printf("[Telegram] GroupId: %v\n", groupId)

	//XMPP init
	xmppClient, err := initXmppClient(conf)
	if err != nil {
		return
	}
	defer xmppClient.Close()

	//IRC loop
	if err := ircCon.Connect(conf.IRC.Server); err != nil {
		log.Printf("[IRC] Failed to connect to %v: %v...\n", conf.IRC.Server, err)
		return
	}
	ircCon.AddCallback("001", func(e *irc.Event) {
		if len(conf.IRC.Identify) != 0 {
			ircCon.Privmsg("NickServ", "identify "+conf.IRC.Identify)
		}
		ircCon.Join(conf.IRC.Channel)
	})
	ircCon.AddCallback("JOIN", func(e *irc.Event) {
		//IRC welcome message
		log.Printf("[IRC] Joined channel %v\n", conf.IRC.Channel)
		//ignore when other people join
		ircCon.ClearCallback("JOIN")
	})
	ircCon.AddCallback("PRIVMSG", func(e *irc.Event) {
		// strip mIRC color codes
		re := regexp.MustCompile("\x1f|\x02|\x03(?:\\d{1,2}(?:,\\d{1,2})?)?")
		msg := re.ReplaceAllString(e.Message(), "")
		//construct/log message
		ircMsg := fmt.Sprintf("<%v> %v", e.Nick, msg)
		log.Printf("[IRC] %v\n", ircMsg)
		//send to Telegram
		if groupId != 0 {
			bot.Send(tgbotapi.NewMessage(groupId, ircMsg))
		}
		//send to Gitter
		gitterCon.Privmsg(conf.Gitter.Channel, ircMsg)
		//send to XMPP
		xmppClient.Send(xmpp.Chat{Remote: conf.XMPP.Muc, Type: "groupchat", Text: ircMsg})
	})
	go ircCon.Loop()

	//Gitter loop
	if err := gitterCon.Connect(conf.Gitter.Server); err != nil {
		log.Printf("[Gitter] Failed to connect to %v: %v...\n", conf.Gitter.Server, err)
		return
	}
	gitterCon.AddCallback("001", func(e *irc.Event) {
		gitterCon.Join(conf.Gitter.Channel)
	})
	gitterCon.AddCallback("JOIN", func(e *irc.Event) {
		//Gitter welcome message
		log.Printf("[Gitter] Joined channel %v\n", conf.Gitter.Channel)
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
				log.Printf("[Gitter Status] %v", gitterMsg)
				return
			}
		} else { //normal messages
			gitterMsg = fmt.Sprintf("<%v> %v", e.Nick, gitterEscape(e.Message()))
		}
		//log message
		log.Printf("[Gitter] %v\n", gitterMsg)
		//send to Telegram
		if groupId != 0 {
			tgmsg := tgbotapi.NewMessage(groupId, gitterMsg)
			if e.Nick == "gitter" { //status messages
				tgmsg.DisableWebPagePreview = true
				tgmsg.DisableNotification = true
			}
			bot.Send(tgmsg)
		}
		//send to IRC
		ircCon.Privmsg(conf.IRC.Channel, gitterMsg)
		//send to XMPP
		xmppClient.Send(xmpp.Chat{Remote: conf.XMPP.Muc, Type: "groupchat", Text: gitterMsg})
	})
	go gitterCon.Loop()

	//XMPP loop
	go func() {
		remotePrefix := conf.XMPP.Muc + "/"
		mucRemote := remotePrefix + conf.XMPP.Nick

		for {
			chat, err := xmppClient.Recv()
			if err != nil {
				log.Printf("[XMPP] Recv error %v, reconnecting every 3 seconds...\n", err)
				xmppClient.Close()
				for {
					time.Sleep(3 * time.Second)
					client, err := initXmppClient(conf)
					if err == nil {
						xmppClient = client
						break
					}
				}
			}
			switch v := chat.(type) {
			case xmpp.Chat:
				if len(v.Text) > 0 && strings.HasPrefix(v.Remote, remotePrefix) && v.Remote != mucRemote {
					nick := v.Remote[len(remotePrefix):]
					log.Printf("[XMPP] <%s> %s\n", nick, v.Text)
					//send to Telegram
					if groupId != 0 {
						bot.Send(tgbotapi.NewMessage(groupId, fmt.Sprintf("<%s> %s", nick, v.Text)))
					}
					//send to IRC
					ircPrivMsg(ircCon, conf.IRC.Channel, nick, v.Text)
					//send to Gitter
					ircPrivMsg(gitterCon, conf.Gitter.Channel, nick, v.Text)
				}
			}
		}
	}()

	//Telegram loop
	for update := range updates {
		//copy variables
		message := update.Message
		if message == nil {
			log.Printf("[Telegram] message == nil\n%v\n", update)
			continue
		}
		chat := message.Chat
		if chat == nil {
			log.Printf("[Telegram] chat == nil\n%v\n", update)
			continue
		}
		name := message.From.UserName
		if len(name) == 0 {
			name = message.From.FirstName
		}
		//TODO: use goroutines if it turns out people are sending a lot of photos
		if len(conf.Telegram.ImgurClientId) > 0 && message.Photo != nil && len(*message.Photo) > 0 {
			photo := (*message.Photo)[len(*message.Photo)-1]
			url, err := bot.GetFileDirectURL(photo.FileID)
			if err != nil {
				log.Printf("GetFileDirectURL error: %v\n", err)
			} else {
				url, err = imgurUploadImageByURL(conf.Telegram.ImgurClientId, url)
				if err != nil {
					log.Printf("imgurUploadImageByURL error: %v\n", err)
				} else {
					if len(message.Caption) > 0 {
						message.Text = fmt.Sprintf("%v %v", message.Caption, url)
					} else {
						message.Text = url
					}
				}
			}
		}
		if len(message.Text) == 0 {
			continue
		}
		//construct/log message
		log.Printf("[Telegram] <%v> %v\n", name, message.Text)
		//check for admin commands
		if stringInSlice(message.From.UserName, strings.Split(conf.Telegram.Admins, " ")) && strings.HasPrefix(message.Text, "/") {
			if message.Text == "/start" && (chat.IsGroup() || chat.IsSuperGroup()) {
				groupId = chat.ID
			} else if message.Text == "/status" {
				bot.Send(tgbotapi.NewMessage(int64(message.From.ID), fmt.Sprintf("groupId: %v, IRC: %v, Gitter: %v", groupId, ircCon.Connected(), gitterCon.Connected())))
			}
		} else if groupId != 0 {
			//forward message to group
			if groupId != chat.ID {
				bot.Send(tgbotapi.NewMessage(groupId, fmt.Sprintf("<%v> %v", name, message.Text)))
			}
			//send to IRC
			ircPrivMsg(ircCon, conf.IRC.Channel, name, message.Text)
			//send to Gitter
			ircPrivMsg(gitterCon, conf.Gitter.Channel, name, message.Text)
			//send to XMPP
			xmppClient.Send(xmpp.Chat{Remote: conf.XMPP.Muc, Type: "groupchat", Text: message.Text})
		} else {
			log.Printf("[Telegam] Use /start to start the bot...\n")
		}
	}
}

func main() {
	fmt.Println("Gitter/IRC Sync Bot, written in Go by mrexodia")
	var conf Config
	if err := configor.Load(&conf, "config.json"); err != nil {
		log.Printf("Error loading config: %v...\n", err)
		return
	}
	goGitterIrcTelegram(conf)
}
