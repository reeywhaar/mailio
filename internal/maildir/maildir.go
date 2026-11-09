// Package maildir imports a Maildir, the Postfix-based mailio's format, into users' mailboxes.
package maildir

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mailio/internal/address"
	"mailio/internal/store"
)

// Folder is one Maildir folder: the top level is INBOX, and Maildir++ subfolders are ".Name".
type Folder struct {
	Dir     string
	Mailbox string
}

// Folders lists the Maildir's folders and the mailbox each goes into.
func Folders(root string) ([]Folder, error) {
	out := []Folder{{Dir: root, Mailbox: store.Inbox}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".") || e.Name() == "." || e.Name() == ".." {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "cur")); err != nil {
			continue
		}
		out = append(out, Folder{Dir: dir, Mailbox: mailboxName(e.Name())})
	}
	return out, nil
}

// mailboxName maps a Maildir++ folder to a mailbox, the common names for the special ones onto
// mailio's: ".Sent Items" files into Sent.
func mailboxName(dir string) string {
	name := strings.ReplaceAll(strings.TrimPrefix(dir, "."), ".", string(store.Delim))
	switch strings.ToLower(name) {
	case "sent", "sent items", "sent messages", "sent mail":
		return "Sent"
	case "junk", "spam", "junk e-mail", "junk email":
		return "Junk"
	case "trash", "deleted items", "deleted messages", "bin":
		return "Trash"
	case "drafts", "draft":
		return "Drafts"
	case "archive", "archives":
		return "Archive"
	}
	return name
}

// Message is one file.
type Message struct {
	Path  string
	Flags []string
	At    time.Time
}

// Messages lists a folder's messages, oldest first. One in new/ has not been seen by any client.
func Messages(dir string) ([]Message, error) {
	var out []Message
	for _, sub := range []string{"cur", "new"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			out = append(out, Message{Path: filepath.Join(dir, sub, e.Name()), Flags: flags(e.Name()), At: info.ModTime()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// flags reads the ":2,FRS" a Maildir file name ends with.
func flags(name string) []string {
	i := strings.LastIndex(name, ":2,")
	if i < 0 {
		return nil
	}
	var out []string
	for _, c := range name[i+3:] {
		switch c {
		case 'S':
			out = append(out, `\Seen`)
		case 'R':
			out = append(out, `\Answered`)
		case 'F':
			out = append(out, `\Flagged`)
		case 'T':
			out = append(out, `\Deleted`)
		case 'D':
			out = append(out, `\Draft`)
		}
	}
	return out
}

// Owner picks which of users a message belongs to, for a Maildir several addresses delivered
// into: whoever the headers say it was sent to, X-Original-To first, which Postfix stamps with the
// address before any alias; for sent mail, whoever it is from. Nil when nobody matches.
func Owner(raw []byte, users []*store.User, sent bool) *store.User {
	m, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil
	}
	keys := []string{"X-Original-To", "Delivered-To", "To", "Cc"}
	if sent {
		keys = []string{"From", "Sender"}
	}
	for _, key := range keys {
		for _, v := range m.Header[key] {
			list, err := mail.ParseAddressList(v)
			if err != nil {
				list = []*mail.Address{{Address: strings.TrimSpace(v)}}
			}
			for _, a := range list {
				addr, err := address.Parse(a.Address)
				if err != nil {
					continue
				}
				for _, u := range users {
					if addr.Domain == u.Domain && addr.Name() == u.Local {
						return u
					}
				}
			}
		}
	}
	return nil
}

// Result counts what an import did, per user and mailbox.
type Result map[string]int

// Import reads every folder of root into users' mailboxes, making any mailbox that is missing.
// A message whose owner cannot be told goes to the first user. dry counts without writing.
func Import(ctx context.Context, st *store.Store, root string, users []*store.User, dry bool) (Result, error) {
	folders, err := Folders(root)
	if err != nil {
		return nil, err
	}
	res := Result{}
	for _, f := range folders {
		msgs, err := Messages(f.Dir)
		if err != nil {
			return nil, err
		}
		batches := map[string][]store.Imported{}
		for _, m := range msgs {
			raw, err := os.ReadFile(m.Path)
			if err != nil {
				return nil, err
			}
			owner := users[0]
			if len(users) > 1 {
				if u := Owner(raw, users, f.Mailbox == "Sent"); u != nil {
					owner = u
				}
			}
			batches[owner.ID] = append(batches[owner.ID], store.Imported{Raw: raw, Flags: m.Flags, At: m.At})
		}
		for _, u := range users {
			batch := batches[u.ID]
			if len(batch) == 0 {
				continue
			}
			res[u.Address()+" "+f.Mailbox] += len(batch)
			if dry {
				continue
			}
			box, err := st.MailboxNamed(ctx, u.ID, f.Mailbox)
			if err != nil {
				if box, err = st.CreateMailbox(ctx, u.ID, f.Mailbox); err != nil {
					return nil, fmt.Errorf("%s: %w", f.Mailbox, err)
				}
			}
			for i := 0; i < len(batch); i += 200 {
				end := min(i+200, len(batch))
				if err := st.Import(ctx, box.ID, batch[i:end]); err != nil {
					return nil, err
				}
			}
		}
	}
	return res, nil
}
