// main.go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"mime/quotedprintable"
	"net/smtp"
	"os"
	"os/user"
	"strings"
	"time"

	ps "github.com/mitchellh/go-ps"
)

var (
	sender      Sender
	subject     string
	dests       []string
	currentUser string
	maxfails    int
	oneshot     *bool
	getStatus   func() ([]byte, error)
)

const (
	moreHelp string = `Start by running the application with no arguments.
	The application will create a credentials file in JSON format. You'll need a
	username and password from Gmail to populate this file. Get those credentials
	from : https://myaccount.google.com/apppasswords	
	`
)

func main() {
	oneshot = flag.Bool("oneshot", false, "Only check one then exit. Useful for cron jobs.")
	interval := flag.Int("interval", 1, "How often to check in minutes.")
	email := flag.String("email", "blue@aquarat.co.za", "Destination e-mail.")
	username := flag.String("username", "you@gmail.com", "Google account username (used to create credentials file).")
	password := flag.String("password", "awesomepassword", "Google account password (used to create credentials file).")
	testemail := flag.Bool("testemail", false, "Send a test e-mail.")
	credsfile := flag.String("creds", "creds.json", "Credentials file path (creds.json by default).")
	moreHelp := flag.Bool("morehelp", false, "Need more help ?")
	maxFailsptr := flag.Uint("maxfails", 5, "Number of failures before a fault is reported.")

	flag.Parse()

	getStatus = actualGetStatus

	if Iamrunning() {
		log.Println("Application already running.")
		os.Exit(1)
	}

	if *moreHelp {
		log.Println(moreHelp)
		os.Exit(1)
	}

	maxfails = int(*maxFailsptr)

	sender = Sender{User: *username, Password: *password}

	if _, err := os.Stat(*credsfile); os.IsNotExist(err) {
		f, err := os.Create(*credsfile)
		defer f.Close()
		if err != nil {
			log.Println("Failed to create credentials file.")
			log.Println(err)
			os.Exit(1)
		}

		someBytes, _ := json.Marshal(sender)
		f.Write(someBytes)
		f.Sync()

		log.Println("Credentials file created, please populate it with real login details and then re-run this app.")
		os.Exit(1)
	}

	f, err := os.Open(*credsfile)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}

	someBytes, err := ioutil.ReadAll(f)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	f.Close()

	err = json.Unmarshal(someBytes, &sender)
	if err != nil {
		log.Println(err)
		log.Println("Credentials file is corrupt :/")
		os.Exit(1)
	}

	userOS, _ := user.Current()
	currentUser = userOS.Username

	hn, _ := os.Hostname()
	subject = "Node Down : " + hn

	dests = []string{*email}

	log.Println("Go!")

	if *testemail {
		sendMail("Test mail")
		os.Exit(0)
	}

	checkCounter := -1
	if *oneshot {
		check(&checkCounter)
		os.Exit(0)
	}

	ticky := time.NewTicker(time.Duration(int64(*interval)) * time.Minute)

	counter := 0
	check(&counter)

	for range ticky.C {
		check(&counter)
		fmt.Print(".")
	}
}

func Iamrunning() (yes bool) {
	pss, err := ps.Processes()
	if err != nil {
		log.Println("Unable to check runninng processes")
		return false
	}

	for _, j := range pss {
		if strings.Contains(j.Executable(), os.Args[0]) {
			return true
		}
	}

	return
}

func sendMail(message string) {
	sender.WritePlainEmail(dests, subject, message)
}

func testGetStatus() (someBytes []byte, statusErr error) {
	return []byte(`{"warping":false,"polling":true,"downloadsStart":1,"downloadsEnd":1,"downloadsCurrent":1,"snapshotsStart":1,"snapshotsEnd":1,"snapshotsCurrent":1,"keepAlive":{"status":200,"timestamp":1548015777354,"message":"In rotation"}}`), nil
}

func actualGetStatus() (someBytes []byte, statusErr error) {
	f, err := os.Open(`/home/` + currentUser + `/.aurad/ipc/status.json`)
	defer f.Close()
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// {"warping":false,"polling":true,"downloadsStart":1,"downloadsEnd":1,"downloadsCurrent":1,"snapshotsStart":1,"snapshotsEnd":1,"snapshotsCurrent":1,"keepAlive":{"status":200,"timestamp":1548015777354,"message":"In rotation"}}
	someBytes, err = ioutil.ReadAll(f)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return someBytes, nil
}

func check(counter *int) {
	defer recover()

	fail := false
	someBytes, err := getStatus()

	status := &Status{}
	if err != nil {
		fail = true
	}

	err = json.Unmarshal(someBytes, status)

	if err != nil {
		fail = true
	} else {
		if !strings.Contains(string(someBytes), "In rotation") ||
			status.KeepAlive.Status != 200 ||
			time.Now().Unix()*1000-status.KeepAlive.Timestamp > 120 {
			fail = true
		} else {
			*counter = 0
		}
	}

	if fail {
		*counter = *counter + 1
		if *counter == maxfails || *oneshot {
			sendMail(string(someBytes))
		}

		fmt.Print("!")
	}

	return
}

const (
	SMTPServer = "smtp.gmail.com"
)

type Status struct {
	Warping          bool `json:"warping"`
	Polling          bool `json:"polling"`
	DownloadsStart   int  `json:"downloadsStart"`
	DownloadsEnd     int  `json:"downloadsEnd"`
	DownloadsCurrent int  `json:"downloadsCurrent"`
	SnapshotsStart   int  `json:"snapshotsStart"`
	SnapshotsEnd     int  `json:"snapshotsEnd"`
	SnapshotsCurrent int  `json:"snapshotsCurrent"`
	KeepAlive        struct {
		Status    int    `json:"status"`
		Timestamp int64  `json:"timestamp"`
		Message   string `json:"message"`
	} `json:"keepAlive"`
}

type Sender struct {
	User     string
	Password string
}

func (sender Sender) WriteEmail(dest []string, contentType, subject, bodyMessage string) {

	header := make(map[string]string)
	header["From"] = sender.User

	receipient := ""

	for _, user := range dest {
		receipient = receipient + user
	}

	header["To"] = receipient
	header["Subject"] = subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = fmt.Sprintf("%s; charset=\"utf-8\"", contentType)
	header["Content-Transfer-Encoding"] = "quoted-printable"
	header["Content-Disposition"] = "inline"

	message := ""

	for key, value := range header {
		message += fmt.Sprintf("%s: %s\r\n", key, value)
	}

	var encodedMessage bytes.Buffer

	finalMessage := quotedprintable.NewWriter(&encodedMessage)
	finalMessage.Write([]byte(bodyMessage))
	finalMessage.Close()

	message += "\r\n" + encodedMessage.String()

	err := smtp.SendMail(SMTPServer+":587",
		smtp.PlainAuth("", sender.User, sender.Password, SMTPServer),
		sender.User, dest, []byte(message))

	if err != nil {

		fmt.Printf("smtp error: %s", err)
		return
	}

	fmt.Println("Mail sent successfully!")
}

func (sender *Sender) WritePlainEmail(dest []string, subject, bodyMessage string) {
	sender.WriteEmail(dest, "text/plain", subject, bodyMessage)
}
