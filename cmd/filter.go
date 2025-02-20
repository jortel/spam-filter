package main

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	INBOX = "INBOX"
	SPAM  = "INBOX.spam"
)

// filterSpam spam is detected by matching the account and/or domain
// to messages found in the INBOX.spam folder. Spam found in the
// INBOX is moved to the INBOX.spam folder.
func filterSpam(client *imapclient.Client) {
	spam := make(map[string]Domain)
	fetchSpam(client, spam)
	fmt.Println("SPAM CATALOG:")
	for _, domain := range spam {
		fmt.Printf("  %s\n", domain.string())
	}

	messages := fetchInbox(client)

	for i := range messages {
		m := messages[i]
		domain := "NONE"
		account := "NONE"
		if len(m.Envelope.Sender) == 0 {
			continue
		}
		domain = m.Envelope.Sender[0].Host
		account = m.Envelope.From[0].Mailbox
		subject := m.Envelope.Subject
		spamDomain := spam[domain]
		if !spamDomain.match(account) {
			continue
		}

		fmt.Printf(
			"FOUND: (%s@%s) '%s' matched: %s\n",
			account,
			domain,
			subject,
			spamDomain.string())

		if promptContinue() {
			moveSpam(client, m)
		}
	}
}

func moveSpam(client *imapclient.Client, m *imapclient.FetchMessageBuffer) {
	fmt.Printf(
		"\nMoving: uid:%d\n",
		m.UID)
	seqSet := imap.SeqSet{}
	seqSet.AddNum(m.SeqNum)
	moveCmd := client.Move(seqSet, SPAM)
	md, err := moveCmd.Wait()
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"\nMoved: uid:%s\n",
		md.SourceUIDs.String())
}

func promptContinue() (cont bool) {
	r := bufio.NewReader(os.Stdin)
	fmt.Printf("MOVE:[y|n]: ")
	ans, _ := r.ReadString('\n')
	ans = strings.TrimSpace(ans)
	if ans == "q" {
		os.Exit(0)
	}
	cont = ans == "y" || ans == ""
	return
}

func fetchInbox(client *imapclient.Client) (messages []*imapclient.FetchMessageBuffer) {
	selectCmd := client.Select(INBOX, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	fetchCmd := client.Fetch(seqSet, options)
	mark := time.Now()
	messages, err = fetchCmd.Collect()
	if err != nil {
		panic(err)
	}
	slices.Reverse(messages)
	fmt.Printf(
		"\nfetch[INBOX]: count: %d, duration: %s\n\n",
		box.NumMessages,
		time.Since(mark))
	return
}

func fetchSpam(client *imapclient.Client, domains map[string]Domain) {
	selectCmd := client.Select(SPAM, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true}
	fetchCmd := client.Fetch(seqSet, options)
	mark := time.Now()
	messages, err := fetchCmd.Collect()
	if err != nil {
		panic(err)
	}

	fmt.Printf(
		"\nfetch[SPAM]: count: %d, duration: %s\n\n",
		box.NumMessages,
		time.Since(mark))

	for i := range messages {
		m := messages[i]
		host := "NONE"
		account := "NONE"
		if len(m.Envelope.Sender) > 0 {
			account = m.Envelope.From[0].Mailbox
			host = m.Envelope.Sender[0].Host
		}
		if inWhiteList(host) {
			continue
		}
		spam := domains[host]
		spam.name = host
		spam.add(account)
		domains[host] = spam
	}
}

type Domain struct {
	name    string
	account map[string]int
	count   int
}

func (d *Domain) add(account string) {
	d.count++
	if d.account == nil {
		d.account = make(map[string]int)
	}
	d.account[account] = d.account[account] + 1
}

func (d *Domain) match(account string) (matched bool) {
	switch d.count {
	case 0:
		break
	case 1:
		_, matched = d.account[account]
	default:
		matched = true
	}
	return
}

func (d *Domain) string() (s string) {
	s = fmt.Sprintf("(%s) nAccount: %d", d.name, len(d.account))
	return
}
