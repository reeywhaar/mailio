package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Inbox is the one mailbox whose name is case-insensitive, and the one every account always has.
const Inbox = "INBOX"

// DefaultMessageBytes is the largest message a new server takes, by SMTP or by IMAP APPEND, until
// the defaults, a domain or an account say otherwise. Larger than most providers send.
const DefaultMessageBytes = 50 << 20

// MessageCeiling is the most any of them can be set to: a message is held in memory whole while
// it is received, signed and stored.
const MessageCeiling = 100 << 20

// stamped is room for what mailio adds to a message it takes: Return-Path, Received,
// Authentication-Results, a DKIM signature.
const stamped = 64 << 10

// Megabytes is a size as the admin interface writes it: 50 MB, 12.5 MB.
func Megabytes(n int64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/(1<<20)), ".0") + " MB"
}

// Delim separates the levels of a mailbox name.
const Delim = '/'

// StandardMailboxes is the set every account starts with, with the special use each announces so a
// client files sent mail and junk without asking.
var StandardMailboxes = []struct{ Name, SpecialUse string }{
	{Inbox, ""},
	{"Sent", `\Sent`},
	{"Drafts", `\Drafts`},
	{"Archive", `\Archive`},
	{"Junk", `\Junk`},
	{"Trash", `\Trash`},
}

// Mailbox is one folder of one account's mail.
type Mailbox struct {
	ID          int64
	AccountID   string
	Name        string
	SpecialUse  string
	Subscribed  bool
	UIDValidity uint32
	UIDNext     uint32
}

// Message is a message without its body: what a mailbox listing needs.
type Message struct {
	UID        uint32
	Flags      []string
	ReceivedAt time.Time
	Size       int64
}

// MailListener hears about every message added to a mailbox, after it is committed and in UID
// order per mailbox, and about messages the store deletes for good itself, which a session did
// not. IMAP is the listener: it tells open sessions.
type MailListener interface {
	Appended(mailboxID int64, uid uint32)
	Expunged(mailboxID int64, uids []uint32)
}

// SetMailListener installs the listener. Set once, before anything is delivered.
func (s *Store) SetMailListener(l MailListener) { s.listener = l }

func createStandardMailboxes(ctx context.Context, db execer, accountID string, now time.Time) error {
	for i, m := range StandardMailboxes {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO mailboxes (account_id, name, special_use, uid_validity) VALUES (?, ?, ?, ?)`,
			accountID, m.Name, m.SpecialUse, uint32(now.Unix())+uint32(i)); err != nil {
			return fmt.Errorf("create mailboxes: %w", err)
		}
	}
	return nil
}

// MailboxName is the stored form of a name: INBOX in capitals whatever the case, and no trailing
// delimiter.
func MailboxName(name string) string {
	name = strings.TrimRight(name, string(Delim))
	if strings.EqualFold(name, Inbox) {
		return Inbox
	}
	return name
}

const mailboxColumns = `id, account_id, name, special_use, subscribed, uid_validity, uid_next`

func scanMailbox(row interface{ Scan(...any) error }) (*Mailbox, error) {
	var m Mailbox
	if err := row.Scan(&m.ID, &m.AccountID, &m.Name, &m.SpecialUse, &m.Subscribed, &m.UIDValidity, &m.UIDNext); err != nil {
		return nil, err
	}
	return &m, nil
}

// Mailboxes returns an account's mailboxes by name.
func (s *Store) Mailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+mailboxColumns+` FROM mailboxes WHERE account_id = ? ORDER BY name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("mailboxes: %w", err)
	}
	defer rows.Close()
	var out []Mailbox
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, fmt.Errorf("mailboxes: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MailboxNamed returns one of an account's mailboxes.
func (s *Store) MailboxNamed(ctx context.Context, accountID, name string) (*Mailbox, error) {
	return mailboxWhere(ctx, s.reader, "account_id = ? AND name = ?", accountID, MailboxName(name))
}

// SpecialMailbox returns the account's mailbox with a special use, such as `\Junk`.
func (s *Store) SpecialMailbox(ctx context.Context, accountID, specialUse string) (*Mailbox, error) {
	return mailboxWhere(ctx, s.reader, "account_id = ? AND special_use = ? ORDER BY id LIMIT 1", accountID, specialUse)
}

func mailboxWhere(ctx context.Context, db queryer, where string, args ...any) (*Mailbox, error) {
	m, err := scanMailbox(db.QueryRowContext(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such mailbox.")
	}
	if err != nil {
		return nil, fmt.Errorf("mailbox: %w", err)
	}
	return m, nil
}

// CreateMailbox makes a mailbox. Its UIDVALIDITY is new, so a client that knew a deleted mailbox
// of the same name does not mistake this one for it.
func (s *Store) CreateMailbox(ctx context.Context, accountID, name string) (*Mailbox, error) {
	name = MailboxName(name)
	if name == "" || strings.ContainsAny(name, "\r\n*%") {
		return nil, Invalid("That is not a mailbox name.")
	}
	validity := uint32(s.Now().Unix())
	res, err := s.writer.ExecContext(ctx,
		`INSERT INTO mailboxes (account_id, name, uid_validity)
		 VALUES (?, ?, max(?, coalesce((SELECT max(uid_validity) + 1 FROM mailboxes WHERE account_id = ?), 0)))`,
		accountID, name, validity, accountID)
	if isUnique(err) {
		return nil, Conflict("There is already a mailbox called %s.", name)
	}
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	id, _ := res.LastInsertId()
	s.changed()
	return mailboxWhere(ctx, s.writer, "id = ?", id)
}

// DeleteMailbox removes a mailbox and every message in it. INBOX stays.
func (s *Store) DeleteMailbox(ctx context.Context, accountID, name string) error {
	name = MailboxName(name)
	if name == Inbox {
		return Invalid("INBOX cannot be deleted.")
	}
	res, err := s.writer.ExecContext(ctx, `DELETE FROM mailboxes WHERE account_id = ? AND name = ?`, accountID, name)
	if err != nil {
		return fmt.Errorf("delete mailbox: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such mailbox.")
	}
	s.changed()
	return nil
}

// RenameMailbox renames a mailbox and everything under it. INBOX keeps its name.
func (s *Store) RenameMailbox(ctx context.Context, accountID, from, to string) error {
	from, to = MailboxName(from), MailboxName(to)
	if from == Inbox || to == Inbox {
		return Invalid("INBOX cannot be renamed.")
	}
	if to == "" {
		return Invalid("That is not a mailbox name.")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename mailbox: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET name = ? WHERE account_id = ? AND name = ?`, to, accountID, from)
	if isUnique(err) {
		return Conflict("There is already a mailbox called %s.", to)
	}
	if err != nil {
		return fmt.Errorf("rename mailbox: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such mailbox.")
	}
	prefix := from + string(Delim)
	if _, err := tx.ExecContext(ctx,
		`UPDATE mailboxes SET name = ? || substr(name, ?) WHERE account_id = ? AND substr(name, 1, ?) = ?`,
		to+string(Delim), len(prefix)+1, accountID, len(prefix), prefix); err != nil {
		if isUnique(err) {
			return Conflict("A mailbox under %s already exists.", to)
		}
		return fmt.Errorf("rename mailbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rename mailbox: %w", err)
	}
	s.changed()
	return nil
}

// SetSubscribed marks a mailbox subscribed or not.
func (s *Store) SetSubscribed(ctx context.Context, accountID, name string, subscribed bool) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE mailboxes SET subscribed = ? WHERE account_id = ? AND name = ?`, subscribed, accountID, MailboxName(name))
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such mailbox.")
	}
	return nil
}

// MailboxStatus is what STATUS and SELECT report about a mailbox.
type MailboxStatus struct {
	Messages, Unseen, Deleted uint32
	Size                      int64
}

// Status counts a mailbox.
func (s *Store) Status(ctx context.Context, mailboxID int64) (*MailboxStatus, error) {
	var st MailboxStatus
	err := s.reader.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(size), 0),
		        coalesce(sum(instr(' ' || flags || ' ', ' \Seen ') = 0), 0),
		        coalesce(sum(instr(' ' || flags || ' ', ' \Deleted ') > 0), 0)
		   FROM messages WHERE mailbox_id = ?`, mailboxID).
		Scan(&st.Messages, &st.Size, &st.Unseen, &st.Deleted)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	return &st, nil
}

// UIDs lists a mailbox's messages in order.
func (s *Store) UIDs(ctx context.Context, mailboxID int64) ([]uint32, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT uid FROM messages WHERE mailbox_id = ? ORDER BY uid`, mailboxID)
	if err != nil {
		return nil, fmt.Errorf("uids: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("uids: %w", err)
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

// Messages returns the messages with these UIDs, in UID order, without bodies. Nil means all.
func (s *Store) Messages(ctx context.Context, mailboxID int64, uids []uint32) ([]Message, error) {
	query := `SELECT uid, flags, received_at, size FROM messages WHERE mailbox_id = ?`
	args := []any{mailboxID}
	if uids != nil {
		if len(uids) == 0 {
			return nil, nil
		}
		query += ` AND uid IN (SELECT value FROM json_each(?))`
		args = append(args, uidsJSON(uids))
	}
	rows, err := s.reader.QueryContext(ctx, query+` ORDER BY uid`, args...)
	if err != nil {
		return nil, fmt.Errorf("messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var (
			m        Message
			flags    string
			received int64
		)
		if err := rows.Scan(&m.UID, &flags, &received, &m.Size); err != nil {
			return nil, fmt.Errorf("messages: %w", err)
		}
		m.Flags = splitFlags(flags)
		m.ReceivedAt = fromUnix(received)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Body returns a message as it arrived.
func (s *Store) Body(ctx context.Context, mailboxID int64, uid uint32) ([]byte, error) {
	var raw []byte
	err := s.reader.QueryRowContext(ctx,
		`SELECT b.raw FROM bodies b JOIN messages m ON m.id = b.message_id WHERE m.mailbox_id = ? AND m.uid = ?`,
		mailboxID, uid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such message.")
	}
	if err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	return raw, nil
}

// Delivery is one mailbox a message is put into, and the flags it starts with there.
type Delivery struct {
	MailboxID int64
	Flags     []string
}

// Deliver puts one message into every mailbox named, in one transaction: an SMTP 250 is said only
// once all of them have it.
func (s *Store) Deliver(ctx context.Context, raw []byte, at time.Time, to []Delivery) error {
	if len(raw) > MessageCeiling+stamped {
		return Invalid("That message is larger than %s.", Megabytes(MessageCeiling))
	}
	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deliver: %w", err)
	}
	defer tx.Rollback()
	type added struct {
		mailbox int64
		uid     uint32
	}
	var done []added
	for _, d := range to {
		uid, err := insertMessage(ctx, tx, d.MailboxID, raw, d.Flags, at, s.Now())
		if err != nil {
			return err
		}
		done = append(done, added{d.MailboxID, uid})
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("deliver: %w", err)
	}
	s.changed()
	for _, a := range done {
		s.appended(a.mailbox, a.uid)
	}
	return nil
}

// Append adds one message to one mailbox, which is IMAP APPEND, and returns its UID.
func (s *Store) Append(ctx context.Context, mailboxID int64, raw []byte, flags []string, at time.Time) (uint32, error) {
	if len(raw) > MessageCeiling+stamped {
		return 0, Invalid("That message is larger than %s.", Megabytes(MessageCeiling))
	}
	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("append: %w", err)
	}
	defer tx.Rollback()
	uid, err := insertMessage(ctx, tx, mailboxID, raw, flags, at, s.Now())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("append: %w", err)
	}
	s.changed()
	s.appended(mailboxID, uid)
	return uid, nil
}

// Copy puts copies of messages into another mailbox, flags and arrival time kept, and returns the
// UIDs they were copied from and given, in the same order.
func (s *Store) Copy(ctx context.Context, from int64, uids []uint32, to int64) (src, dest []uint32, err error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("copy: %w", err)
	}
	defer tx.Rollback()
	for _, uid := range uids {
		var (
			flags    string
			received int64
			raw      []byte
		)
		err := tx.QueryRowContext(ctx,
			`SELECT m.flags, m.received_at, b.raw FROM messages m JOIN bodies b ON b.message_id = m.id
			  WHERE m.mailbox_id = ? AND m.uid = ?`, from, uid).Scan(&flags, &received, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("copy: %w", err)
		}
		n, err := insertMessage(ctx, tx, to, raw, splitFlags(flags), fromUnix(received), s.Now())
		if err != nil {
			return nil, nil, err
		}
		src = append(src, uid)
		dest = append(dest, n)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("copy: %w", err)
	}
	s.changed()
	for _, uid := range dest {
		s.appended(to, uid)
	}
	return src, dest, nil
}

func (s *Store) appended(mailboxID int64, uid uint32) {
	if s.listener != nil {
		s.listener.Appended(mailboxID, uid)
	}
}

// insertMessage adds one message to a mailbox: at is when it arrived, which a copy keeps, and
// added when it entered this mailbox, which Trash counts from.
func insertMessage(ctx context.Context, tx *sql.Tx, mailboxID int64, raw []byte, flags []string, at, added time.Time) (uint32, error) {
	var uid uint32
	err := tx.QueryRowContext(ctx,
		`UPDATE mailboxes SET uid_next = uid_next + 1 WHERE id = ? RETURNING uid_next - 1`, mailboxID).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, NotFound("There is no such mailbox.")
	}
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages (mailbox_id, uid, flags, received_at, size, added_at) VALUES (?, ?, ?, ?, ?, ?)`,
		mailboxID, uid, joinFlags(NormalizeFlags(flags)), unix(at), len(raw), unix(added))
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO bodies (message_id, raw) VALUES (?, ?)`, id, raw); err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	return uid, nil
}

// SetFlags replaces a message's flags.
func (s *Store) SetFlags(ctx context.Context, mailboxID int64, uid uint32, flags []string) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE messages SET flags = ? WHERE mailbox_id = ? AND uid = ? AND flags <> ?`,
		joinFlags(NormalizeFlags(flags)), mailboxID, uid, joinFlags(NormalizeFlags(flags)))
	if err != nil {
		return fmt.Errorf("set flags: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
	}
	return nil
}

// Expunge deletes messages for good.
func (s *Store) Expunge(ctx context.Context, mailboxID int64, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	_, err := s.writer.ExecContext(ctx,
		`DELETE FROM messages WHERE mailbox_id = ? AND uid IN (SELECT value FROM json_each(?))`,
		mailboxID, uidsJSON(uids))
	if err != nil {
		return fmt.Errorf("expunge: %w", err)
	}
	s.changed()
	return nil
}

// trashDays is how long an account's Trash keeps a message, in a query joining the account as u
// and its domain as d: its own, its domain's, or the default. 0 keeps it forever.
const trashDays = `coalesce(u.trash_days, d.trash_days, (SELECT trash_days FROM settings WHERE id = 1))`

// EmptyTrash deletes for good what has been in an account's Trash longer than the account keeps
// it, and tells the listener. It answers how many went.
func (s *Store) EmptyTrash(ctx context.Context) (int, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT m.mailbox_id, m.uid FROM messages m
		   JOIN mailboxes b ON b.id = m.mailbox_id
		   JOIN accounts u ON u.id = b.account_id
		   JOIN domains d ON d.id = u.domain_id
		  WHERE b.special_use = '\Trash' AND `+trashDays+` > 0 AND m.added_at <= ? - `+trashDays+` * 86400
		  ORDER BY m.mailbox_id, m.uid`, unix(s.Now()))
	if err != nil {
		return 0, fmt.Errorf("empty trash: %w", err)
	}
	stale := map[int64][]uint32{}
	var boxes []int64
	for rows.Next() {
		var (
			box int64
			uid uint32
		)
		if err := rows.Scan(&box, &uid); err != nil {
			rows.Close()
			return 0, fmt.Errorf("empty trash: %w", err)
		}
		if stale[box] == nil {
			boxes = append(boxes, box)
		}
		stale[box] = append(stale[box], uid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("empty trash: %w", err)
	}
	n := 0
	for _, box := range boxes {
		if err := s.Expunge(ctx, box, stale[box]); err != nil {
			return n, err
		}
		n += len(stale[box])
		if s.listener != nil {
			s.listener.Expunged(box, stale[box])
		}
	}
	return n, nil
}

func uidsJSON(uids []uint32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, uid := range uids {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprint(&b, uid)
	}
	b.WriteByte(']')
	return b.String()
}

// The system flags, in the case IMAP writes them. A keyword is kept as first written.
var systemFlags = map[string]string{
	`\seen`:     `\Seen`,
	`\answered`: `\Answered`,
	`\flagged`:  `\Flagged`,
	`\deleted`:  `\Deleted`,
	`\draft`:    `\Draft`,
}

// NormalizeFlags drops duplicates regardless of case, writes system flags in their own case, and
// sorts. \Recent is dropped: it is a session's, never stored.
func NormalizeFlags(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, f := range in {
		f = strings.TrimSpace(f)
		key := strings.ToLower(f)
		if f == "" || key == `\recent` || seen[key] || strings.ContainsAny(f, " ()\"") {
			continue
		}
		seen[key] = true
		if canon, ok := systemFlags[key]; ok {
			f = canon
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func joinFlags(flags []string) string { return strings.Join(flags, " ") }

func splitFlags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

// Imported is one message for Import.
type Imported struct {
	Raw   []byte
	Flags []string
	At    time.Time
}

// Import adds many messages to a mailbox in one transaction: a commit per message, each waiting
// on the disk, would make a mailbox of thousands an afternoon.
func (s *Store) Import(ctx context.Context, mailboxID int64, msgs []Imported) error {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	defer tx.Rollback()
	var uids []uint32
	for _, m := range msgs {
		uid, err := insertMessage(ctx, tx, mailboxID, m.Raw, m.Flags, m.At, s.Now())
		if err != nil {
			return err
		}
		uids = append(uids, uid)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	s.changed()
	for _, uid := range uids {
		s.appended(mailboxID, uid)
	}
	return nil
}
