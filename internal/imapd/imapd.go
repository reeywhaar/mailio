// Package imapd serves accounts' mail over IMAP, from the store.
//
// The messages are in SQLite; what lives here is each open mailbox's ordered list of UIDs and the
// tracker that turns changes into the untagged responses every session watching it is owed.
// mailio is one process, so every write passes through here or through the store's listener, and
// the list never goes stale.
package imapd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"mailio/internal/app"
	"mailio/internal/store"
)

// Server is the IMAP backend and the registry of open mailboxes.
type Server struct {
	st  *store.Store
	log *slog.Logger

	mu   sync.Mutex
	open map[int64]*mailbox
	srv  *imapserver.Server
}

// mailbox is one mailbox some session has selected: its UIDs in order, and the tracker whose
// numbering they are.
type mailbox struct {
	mu      sync.Mutex
	uids    []imap.UID
	tracker *imapserver.MailboxTracker
}

// New makes the backend and has the store tell it about every message added anywhere.
func New(st *store.Store, log *slog.Logger) *Server {
	s := &Server{st: st, log: log, open: make(map[int64]*mailbox)}
	s.srv = imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &session{srv: s, ip: peerIP(c.NetConn())}, nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1:  {},
			imap.CapIMAP4rev2:  {},
			imap.CapSpecialUse: {},
		},
		Logger: logger{log},
		// The listener is TLS; the library cannot see that through the wrapper.
		InsecureAuth: true,
	})
	st.SetMailListener(s)
	return s
}

// Appended is the store telling us a message was committed, in UID order per mailbox.
func (s *Server) Appended(mailboxID int64, uid uint32) {
	s.mu.Lock()
	mb := s.open[mailboxID]
	s.mu.Unlock()
	if mb == nil {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	// Loaded after the commit and before this call, the list already has it.
	if n := len(mb.uids); n > 0 && mb.uids[n-1] >= imap.UID(uid) {
		return
	}
	mb.uids = append(mb.uids, imap.UID(uid))
	mb.tracker.QueueNumMessages(uint32(len(mb.uids)))
}

// Expunged is the store telling us it deleted messages for good itself, emptying a Trash.
func (s *Server) Expunged(mailboxID int64, uids []uint32) {
	s.mu.Lock()
	mb := s.open[mailboxID]
	s.mu.Unlock()
	if mb == nil {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.remove(uids)
}

// remove takes messages out of the list and queues the EXPUNGE every watching session is owed.
// The caller holds mb.mu. A UID already gone is skipped, so hearing of one twice is harmless.
func (mb *mailbox) remove(uids []uint32) {
	gone := make(map[imap.UID]bool, len(uids))
	for _, u := range uids {
		gone[imap.UID(u)] = true
	}
	// From the end, so each sequence number is still right when it is sent.
	for i := len(mb.uids) - 1; i >= 0; i-- {
		if gone[mb.uids[i]] {
			mb.tracker.QueueExpunge(uint32(i) + 1)
			mb.uids = append(mb.uids[:i], mb.uids[i+1:]...)
		}
	}
}

// mailboxFor opens a mailbox's list, loading it on first use. Holding s.mu across the load is
// what stops two sessions loading it twice, and an append being heard before the list exists.
func (s *Server) mailboxFor(ctx context.Context, id int64) (*mailbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uids, err := s.st.UIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	if mb := s.open[id]; mb != nil {
		mb.catchUp(uids)
		return mb, nil
	}
	mb := &mailbox{uids: make([]imap.UID, len(uids)), tracker: imapserver.NewMailboxTracker(uint32(len(uids)))}
	for i, u := range uids {
		mb.uids[i] = imap.UID(u)
	}
	s.open[id] = mb
	return mb, nil
}

// catchUp adds messages another process wrote, `mailio import` say, which no listener heard of.
// They have UIDs past the list's last, because UIDs only grow.
func (mb *mailbox) catchUp(uids []uint32) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	var last imap.UID
	if n := len(mb.uids); n > 0 {
		last = mb.uids[n-1]
	}
	grew := false
	for _, u := range uids {
		if imap.UID(u) > last {
			mb.uids = append(mb.uids, imap.UID(u))
			grew = true
		}
	}
	if grew {
		mb.tracker.QueueNumMessages(uint32(len(mb.uids)))
	}
}

func (s *Server) forget(id int64) {
	s.mu.Lock()
	delete(s.open, id)
	s.mu.Unlock()
}

// Serve answers IMAP on ln, which is already TLS: mailio listens on 993 only, so a password never
// crosses the wire in the clear.
func (s *Server) Serve(ln net.Listener) error { return s.srv.Serve(ln) }

// Close stops listening and closes every connection.
func (s *Server) Close() error { return s.srv.Close() }

// Listen is 993 with TLS from cfg.
func Listen(addr string, cfg *tls.Config) (net.Listener, error) {
	return tls.Listen("tcp", addr, cfg)
}

type logger struct{ log *slog.Logger }

func (l logger) Printf(format string, args ...any) {
	l.log.Warn("imap: " + fmt.Sprintf(format, args...))
}

func peerIP(c net.Conn) string {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return c.RemoteAddr().String()
	}
	return host
}

// session is one connection.
type session struct {
	srv     *Server
	ip      string
	account *store.Account
	sel     *selected
}

// selected is the mailbox a session has open.
type selected struct {
	box       store.Mailbox
	mb        *mailbox
	tracker   *imapserver.SessionTracker
	readOnly  bool
	searchRes imap.UIDSet
}

var _ imapserver.SessionIMAP4rev2 = (*session)(nil)
var _ imapserver.SessionAppendLimit = (*session)(nil)

func ctx() context.Context { return context.Background() }

func (s *session) Close() error {
	if s.sel != nil {
		s.sel.tracker.Close()
		s.sel = nil
	}
	return nil
}

func (s *session) Login(username, password string) error {
	u, err := s.srv.st.Authenticate(ctx(), username, password)
	if errors.Is(err, store.ErrNotFound) {
		s.srv.log.Warn(app.PasswordRefused, "ip", s.ip, "via", "imap")
		// Slows a guesser on this connection; fail2ban stops the rest.
		time.Sleep(time.Second)
		return imapserver.ErrAuthFailed
	}
	if err != nil {
		return err
	}
	s.account = u
	s.srv.log.Info("imap signed in", "account", u.ID)
	return nil
}

// AppendLimit is asked before sign-in too, for CAPABILITY, where it is the most any account takes.
func (s *session) AppendLimit() uint32 { return uint32(s.limit()) }

// limit is the largest message the account takes, read each time: a change in the admin
// interface applies to a session already open.
func (s *session) limit() int64 {
	if s.account == nil {
		return store.MessageCeiling
	}
	u, err := s.srv.st.AccountByID(ctx(), s.account.ID)
	if err != nil {
		return s.account.MessageLimit
	}
	return u.MessageLimit
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{Personal: []imap.NamespaceDescriptor{{Delim: store.Delim}}}, nil
}

// box finds one of the account's mailboxes, answering IMAP's way when there is none.
func (s *session) box(name string, code imap.ResponseCode) (*store.Mailbox, error) {
	m, err := s.srv.st.MailboxNamed(ctx(), s.account.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: "No such mailbox"}
	}
	return m, err
}

// refused turns a store refusal into IMAP's NO, with the store's sentence.
func refused(err error) error {
	var code imap.ResponseCode
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = imap.ResponseCodeNonExistent
	case errors.Is(err, store.ErrConflict):
		code = imap.ResponseCodeAlreadyExists
	case errors.Is(err, store.ErrInvalid):
		code = imap.ResponseCodeCannot
	default:
		return err
	}
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: err.Error()}
}

func (s *session) Create(name string, _ *imap.CreateOptions) error {
	_, err := s.srv.st.CreateMailbox(ctx(), s.account.ID, name)
	return refused(err)
}

func (s *session) Delete(name string) error {
	m, err := s.box(name, imap.ResponseCodeNonExistent)
	if err != nil {
		return err
	}
	if err := s.srv.st.DeleteMailbox(ctx(), s.account.ID, name); err != nil {
		return refused(err)
	}
	s.srv.forget(m.ID)
	return nil
}

func (s *session) Rename(from, to string, _ *imap.RenameOptions) error {
	return refused(s.srv.st.RenameMailbox(ctx(), s.account.ID, from, to))
}

func (s *session) Subscribe(name string) error {
	return refused(s.srv.st.SetSubscribed(ctx(), s.account.ID, name, true))
}

func (s *session) Unsubscribe(name string) error {
	return refused(s.srv.st.SetSubscribed(ctx(), s.account.ID, name, false))
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: store.Delim})
	}
	boxes, err := s.srv.st.Mailboxes(ctx(), s.account.ID)
	if err != nil {
		return err
	}
	parents := make(map[string]bool)
	for _, b := range boxes {
		for i := len(b.Name) - 1; i > 0; i-- {
			if b.Name[i] == store.Delim {
				parents[b.Name[:i]] = true
			}
		}
	}
	for _, b := range boxes {
		match := false
		for _, p := range patterns {
			if imapserver.MatchList(b.Name, store.Delim, ref, p) {
				match = true
				break
			}
		}
		if !match || (options.SelectSubscribed && !b.Subscribed) || (options.SelectSpecialUse && b.SpecialUse == "") {
			continue
		}
		data := imap.ListData{Mailbox: b.Name, Delim: store.Delim}
		if parents[b.Name] {
			data.Attrs = append(data.Attrs, imap.MailboxAttrHasChildren)
		} else {
			data.Attrs = append(data.Attrs, imap.MailboxAttrHasNoChildren)
		}
		if b.Subscribed {
			data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
		}
		// Always, not only when asked: a client that never asks still files Sent and Junk right.
		if b.SpecialUse != "" {
			data.Attrs = append(data.Attrs, imap.MailboxAttr(b.SpecialUse))
		}
		if options.ReturnStatus != nil {
			st, err := s.status(&b, options.ReturnStatus)
			if err != nil {
				return err
			}
			data.Status = st
		}
		if err := w.WriteList(&data); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) Status(name string, options *imap.StatusOptions) (*imap.StatusData, error) {
	m, err := s.box(name, imap.ResponseCodeNonExistent)
	if err != nil {
		return nil, err
	}
	return s.status(m, options)
}

func (s *session) status(m *store.Mailbox, options *imap.StatusOptions) (*imap.StatusData, error) {
	st, err := s.srv.st.Status(ctx(), m.ID)
	if err != nil {
		return nil, err
	}
	data := &imap.StatusData{Mailbox: m.Name}
	if options.NumMessages {
		data.NumMessages = &st.Messages
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(m.UIDNext)
	}
	if options.UIDValidity {
		data.UIDValidity = m.UIDValidity
	}
	if options.NumUnseen {
		data.NumUnseen = &st.Unseen
	}
	if options.NumDeleted {
		data.NumDeleted = &st.Deleted
	}
	if options.Size {
		data.Size = &st.Size
	}
	if options.NumRecent {
		zero := uint32(0)
		data.NumRecent = &zero
	}
	return data, nil
}

func (s *session) Append(name string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	m, err := s.box(name, imap.ResponseCodeTryCreate)
	if err != nil {
		return nil, err
	}
	if limit := s.limit(); r.Size() > limit {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeTooBig,
			Text: fmt.Sprintf("Message too big: %s takes up to %s", s.account.Address(), store.Megabytes(limit))}
	}
	raw := make([]byte, r.Size())
	if _, err := ioReadFull(r, raw); err != nil {
		return nil, err
	}
	at := options.Time
	if at.IsZero() {
		at = time.Now()
	}
	uid, err := s.srv.st.Append(ctx(), m.ID, raw, flagStrings(options.Flags), at)
	if err != nil {
		return nil, refused(err)
	}
	return &imap.AppendData{UID: imap.UID(uid), UIDValidity: m.UIDValidity}, nil
}

var permanentFlags = []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft}

func (s *session) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if s.sel != nil {
		s.sel.tracker.Close()
		s.sel = nil
	}
	m, err := s.box(name, imap.ResponseCodeNonExistent)
	if err != nil {
		return nil, err
	}
	mb, err := s.srv.mailboxFor(ctx(), m.ID)
	if err != nil {
		return nil, err
	}
	msgs, err := s.srv.st.Messages(ctx(), m.ID, nil)
	if err != nil {
		return nil, err
	}

	mb.mu.Lock()
	defer mb.mu.Unlock()
	s.sel = &selected{box: *m, mb: mb, tracker: mb.tracker.NewSession(), readOnly: options.ReadOnly}

	var firstUnseen uint32
	unseen := make(map[imap.UID]bool)
	for _, msg := range msgs {
		if !hasFlag(msg.Flags, `\Seen`) {
			unseen[imap.UID(msg.UID)] = true
		}
	}
	for i, uid := range mb.uids {
		if unseen[uid] {
			firstUnseen = uint32(i) + 1
			break
		}
	}
	uidNext := imap.UID(m.UIDNext)
	if n := len(mb.uids); n > 0 && mb.uids[n-1] >= uidNext {
		uidNext = mb.uids[n-1] + 1
	}
	return &imap.SelectData{
		Flags:             permanentFlags,
		PermanentFlags:    append(append([]imap.Flag{}, permanentFlags...), imap.FlagWildcard),
		NumMessages:       uint32(len(mb.uids)),
		FirstUnseenSeqNum: firstUnseen,
		UIDNext:           uidNext,
		UIDValidity:       m.UIDValidity,
	}, nil
}

func (s *session) Unselect() error {
	if s.sel != nil {
		s.sel.tracker.Close()
		s.sel = nil
	}
	return nil
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if s.sel == nil {
		return nil
	}
	return s.sel.tracker.Poll(w, allowExpunge)
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.sel == nil {
		<-stop
		return nil
	}
	return s.sel.tracker.Idle(w, stop)
}

// target is one message a command names: its sequence number in this session's view, and UID.
type target struct {
	seq uint32 // in the mailbox's own numbering
	enc uint32 // in this session's, which lags until it is told of expunges
	uid imap.UID
}

// targets resolves a sequence or UID set against the list, under its lock, into a snapshot the
// command then works from.
func (sel *selected) targets(numSet imap.NumSet) []target {
	sel.mb.mu.Lock()
	defer sel.mb.mu.Unlock()
	numSet = sel.static(numSet)
	var out []target
	for i, uid := range sel.mb.uids {
		seq := uint32(i) + 1
		enc := sel.tracker.EncodeSeqNum(seq)
		var in bool
		switch set := numSet.(type) {
		case imap.SeqSet:
			in = enc != 0 && set.Contains(enc)
		case imap.UIDSet:
			in = set.Contains(uid)
		}
		if in {
			out = append(out, target{seq: seq, enc: enc, uid: uid})
		}
	}
	return out
}

// static resolves "*" and SEARCHRES's "$" into numbers. Called with the list locked.
func (sel *selected) static(numSet imap.NumSet) imap.NumSet {
	if imap.IsSearchRes(numSet) {
		return sel.searchRes
	}
	switch set := numSet.(type) {
	case imap.SeqSet:
		max := uint32(len(sel.mb.uids))
		for i := range set {
			staticRange(&set[i].Start, &set[i].Stop, max)
		}
	case imap.UIDSet:
		var max uint32
		if n := len(sel.mb.uids); n > 0 {
			max = uint32(sel.mb.uids[n-1])
		}
		for i := range set {
			staticRange((*uint32)(&set[i].Start), (*uint32)(&set[i].Stop), max)
		}
	}
	return numSet
}

func staticRange(start, stop *uint32, max uint32) {
	dyn := false
	if *start == 0 {
		*start, dyn = max, true
	}
	if *stop == 0 {
		*stop, dyn = max, true
	}
	if dyn && *start > *stop {
		*start, *stop = *stop, *start
	}
}

// seqOf is a UID's current place in the list, or 0. Called with the list locked.
func (mb *mailbox) seqOf(uid imap.UID) uint32 {
	i := sort.Search(len(mb.uids), func(i int) bool { return mb.uids[i] >= uid })
	if i < len(mb.uids) && mb.uids[i] == uid {
		return uint32(i) + 1
	}
	return 0
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	return s.fetch(w, s.sel.targets(numSet), options)
}

func (s *session) fetch(w *imapserver.FetchWriter, targets []target, options *imap.FetchOptions) error {
	sel := s.sel
	markSeen := false
	for _, bs := range options.BodySection {
		if !bs.Peek {
			markSeen = !sel.readOnly
			break
		}
	}
	needBody := options.Envelope || options.BodyStructure != nil || len(options.BodySection) > 0 ||
		len(options.BinarySection) > 0 || len(options.BinarySectionSize) > 0

	uids := make([]uint32, len(targets))
	for i, t := range targets {
		uids[i] = uint32(t.uid)
	}
	msgs, err := s.srv.st.Messages(ctx(), sel.box.ID, uids)
	if err != nil {
		return err
	}
	byUID := make(map[imap.UID]*store.Message, len(msgs))
	for i := range msgs {
		byUID[imap.UID(msgs[i].UID)] = &msgs[i]
	}

	for _, t := range targets {
		msg := byUID[t.uid]
		if msg == nil || t.enc == 0 {
			continue // expunged meanwhile
		}
		var raw []byte
		if needBody {
			if raw, err = s.srv.st.Body(ctx(), sel.box.ID, uint32(t.uid)); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return err
			}
		}
		if markSeen && !hasFlag(msg.Flags, `\Seen`) {
			msg.Flags = append(msg.Flags, `\Seen`)
			if err := s.setFlags(t.uid, msg.Flags, nil); err != nil {
				return err
			}
		}
		if err := writeMessage(w.CreateMessage(t.enc), t.uid, msg, raw, options); err != nil {
			return err
		}
	}
	return nil
}

// setFlags stores a message's flags and tells every session watching but source.
func (s *session) setFlags(uid imap.UID, flags []string, source *imapserver.SessionTracker) error {
	sel := s.sel
	flags = store.NormalizeFlags(flags)
	if err := s.srv.st.SetFlags(ctx(), sel.box.ID, uint32(uid), flags); err != nil {
		return err
	}
	sel.mb.mu.Lock()
	defer sel.mb.mu.Unlock()
	if seq := sel.mb.seqOf(uid); seq != 0 {
		sel.mb.tracker.QueueMessageFlags(seq, uid, imapFlags(flags), source)
	}
	return nil
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, _ *imap.StoreOptions) error {
	sel := s.sel
	if sel.readOnly {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot, Text: "Mailbox is read-only"}
	}
	targets := sel.targets(numSet)
	uids := make([]uint32, len(targets))
	for i, t := range targets {
		uids[i] = uint32(t.uid)
	}
	msgs, err := s.srv.st.Messages(ctx(), sel.box.ID, uids)
	if err != nil {
		return err
	}
	change := flagStrings(flags.Flags)
	for _, msg := range msgs {
		var next []string
		switch flags.Op {
		case imap.StoreFlagsSet:
			next = change
		case imap.StoreFlagsAdd:
			next = append(append([]string{}, msg.Flags...), change...)
		case imap.StoreFlagsDel:
			for _, f := range msg.Flags {
				if !hasFlag(change, f) {
					next = append(next, f)
				}
			}
		}
		if err := s.setFlags(imap.UID(msg.UID), next, sel.tracker); err != nil {
			return err
		}
	}
	if flags.Silent {
		return nil
	}
	return s.fetch(w, targets, &imap.FetchOptions{Flags: true})
}

func (s *session) Expunge(_ *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	sel := s.sel
	if sel.readOnly {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot, Text: "Mailbox is read-only"}
	}
	msgs, err := s.srv.st.Messages(ctx(), sel.box.ID, nil)
	if err != nil {
		return err
	}
	var gone []uint32
	for _, m := range msgs {
		if hasFlag(m.Flags, `\Deleted`) && (uids == nil || uids.Contains(imap.UID(m.UID))) {
			gone = append(gone, m.UID)
		}
	}
	return s.expunge(gone)
}

// expunge deletes messages and queues the EXPUNGE every watching session is owed, this one
// included: the server polls before the tagged OK, so this session hears it there.
func (s *session) expunge(uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	sel := s.sel
	sel.mb.mu.Lock()
	defer sel.mb.mu.Unlock()
	if err := s.srv.st.Expunge(ctx(), sel.box.ID, uids); err != nil {
		return err
	}
	sel.mb.remove(uids)
	return nil
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	data, _, err := s.copy(numSet, dest)
	return data, err
}

func (s *session) copy(numSet imap.NumSet, dest string) (*imap.CopyData, []uint32, error) {
	sel := s.sel
	to, err := s.box(dest, imap.ResponseCodeTryCreate)
	if err != nil {
		return nil, nil, err
	}
	targets := sel.targets(numSet)
	uids := make([]uint32, len(targets))
	for i, t := range targets {
		uids[i] = uint32(t.uid)
	}
	src, dst, err := s.srv.st.Copy(ctx(), sel.box.ID, uids, to.ID)
	if err != nil {
		return nil, nil, refused(err)
	}
	var srcSet, dstSet imap.UIDSet
	for i := range src {
		srcSet.AddNum(imap.UID(src[i]))
		dstSet.AddNum(imap.UID(dst[i]))
	}
	return &imap.CopyData{UIDValidity: to.UIDValidity, SourceUIDs: srcSet, DestUIDs: dstSet}, src, nil
}

func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	if s.sel.readOnly {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot, Text: "Mailbox is read-only"}
	}
	data, src, err := s.copy(numSet, dest)
	if err != nil {
		return err
	}
	if err := w.WriteCopyData(data); err != nil {
		return err
	}
	return s.expunge(src)
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	sel := s.sel
	msgs, err := s.srv.st.Messages(ctx(), sel.box.ID, nil)
	if err != nil {
		return nil, err
	}
	byUID := make(map[imap.UID]*store.Message, len(msgs))
	for i := range msgs {
		byUID[imap.UID(msgs[i].UID)] = &msgs[i]
	}

	sel.mb.mu.Lock()
	sel.staticCriteria(criteria)
	type entry struct {
		enc uint32
		uid imap.UID
	}
	list := make([]entry, len(sel.mb.uids))
	for i, uid := range sel.mb.uids {
		list[i] = entry{sel.tracker.EncodeSeqNum(uint32(i) + 1), uid}
	}
	sel.mb.mu.Unlock()

	var (
		data   imap.SearchData
		seqSet imap.SeqSet
		uidSet imap.UIDSet
	)
	for _, e := range list {
		msg := byUID[e.uid]
		if msg == nil {
			continue
		}
		c := &candidate{s: s, uid: e.uid, msg: msg}
		if !c.match(e.enc, criteria) {
			continue
		}
		uidSet.AddNum(e.uid)
		var num uint32
		switch kind {
		case imapserver.NumKindSeq:
			if e.enc == 0 {
				continue
			}
			seqSet.AddNum(e.enc)
			num = e.enc
		case imapserver.NumKindUID:
			num = uint32(e.uid)
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		if num > data.Max {
			data.Max = num
		}
		data.Count++
	}
	if kind == imapserver.NumKindSeq {
		data.All = seqSet
	} else {
		data.All = uidSet
	}
	if options.ReturnSave {
		sel.searchRes = uidSet
	}
	return &data, nil
}

// staticCriteria resolves "*" and "$" throughout. Called with the list locked.
func (sel *selected) staticCriteria(c *imap.SearchCriteria) {
	seqs := make([]imap.SeqSet, 0, len(c.SeqNum))
	for _, set := range c.SeqNum {
		switch set := sel.static(set).(type) {
		case imap.SeqSet:
			seqs = append(seqs, set)
		case imap.UIDSet:
			c.UID = append(c.UID, set)
		}
	}
	c.SeqNum = seqs
	for i, set := range c.UID {
		c.UID[i] = sel.static(set).(imap.UIDSet)
	}
	for i := range c.Not {
		sel.staticCriteria(&c.Not[i])
	}
	for i := range c.Or {
		sel.staticCriteria(&c.Or[i][0])
		sel.staticCriteria(&c.Or[i][1])
	}
}
