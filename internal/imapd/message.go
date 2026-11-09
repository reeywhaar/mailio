package imapd

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"mailio/internal/store"
)

func writeMessage(w *imapserver.FetchResponseWriter, uid imap.UID, msg *store.Message, raw []byte, options *imap.FetchOptions) error {
	w.WriteUID(uid)
	if options.Flags {
		w.WriteFlags(imapFlags(msg.Flags))
	}
	if options.InternalDate {
		w.WriteInternalDate(msg.ReceivedAt)
	}
	if options.RFC822Size {
		w.WriteRFC822Size(msg.Size)
	}
	if options.Envelope {
		w.WriteEnvelope(envelope(raw))
	}
	if options.BodyStructure != nil {
		w.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
	}
	for _, bs := range options.BodySection {
		buf := imapserver.ExtractBodySection(bytes.NewReader(raw), bs)
		if err := writeSection(w.WriteBodySection(bs, int64(len(buf))), buf); err != nil {
			return err
		}
	}
	for _, bs := range options.BinarySection {
		buf := imapserver.ExtractBinarySection(bytes.NewReader(raw), bs)
		if err := writeSection(w.WriteBinarySection(bs, int64(len(buf))), buf); err != nil {
			return err
		}
	}
	for _, bss := range options.BinarySectionSize {
		w.WriteBinarySectionSize(bss, imapserver.ExtractBinarySectionSize(bytes.NewReader(raw), bss))
	}
	return w.Close()
}

func writeSection(wc io.WriteCloser, buf []byte) error {
	_, err := wc.Write(buf)
	if cerr := wc.Close(); err == nil {
		err = cerr
	}
	return err
}

func envelope(raw []byte) *imap.Envelope {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil
	}
	return imapserver.ExtractEnvelope(h)
}

// candidate is one message being searched; its body is read only if a criterion needs it.
type candidate struct {
	s   *session
	uid imap.UID
	msg *store.Message
	raw []byte
	err error
}

func (c *candidate) body() []byte {
	if c.raw == nil && c.err == nil {
		c.raw, c.err = c.s.srv.st.Body(ctx(), c.s.sel.box.ID, uint32(c.uid))
	}
	return c.raw
}

func (c *candidate) entity() *gomessage.Entity {
	e, _ := gomessage.Read(bytes.NewReader(c.body()))
	if e == nil {
		e, _ = gomessage.New(gomessage.Header{}, bytes.NewReader(nil))
	}
	return e
}

func (c *candidate) match(seq uint32, cr *imap.SearchCriteria) bool {
	for _, set := range cr.SeqNum {
		if seq == 0 || !set.Contains(seq) {
			return false
		}
	}
	for _, set := range cr.UID {
		if !set.Contains(c.uid) {
			return false
		}
	}
	if !matchDate(c.msg.ReceivedAt, cr.Since, cr.Before) {
		return false
	}
	for _, f := range cr.Flag {
		if !hasFlag(c.msg.Flags, string(f)) {
			return false
		}
	}
	for _, f := range cr.NotFlag {
		if hasFlag(c.msg.Flags, string(f)) {
			return false
		}
	}
	if cr.Larger != 0 && c.msg.Size <= cr.Larger {
		return false
	}
	if cr.Smaller != 0 && c.msg.Size >= cr.Smaller {
		return false
	}
	if len(cr.Header) > 0 || !cr.SentSince.IsZero() || !cr.SentBefore.IsZero() {
		h := mail.Header{Header: c.entity().Header}
		for _, f := range cr.Header {
			if !matchFields(h.FieldsByKey(f.Key), f.Value) {
				return false
			}
		}
		if !cr.SentSince.IsZero() || !cr.SentBefore.IsZero() {
			t, err := h.Date()
			if err != nil || !matchDate(t, cr.SentSince, cr.SentBefore) {
				return false
			}
		}
	}
	for _, text := range cr.Text {
		if !matchEntity(c.entity(), text, true) {
			return false
		}
	}
	for _, text := range cr.Body {
		if !matchEntity(c.entity(), text, false) {
			return false
		}
	}
	for i := range cr.Not {
		if c.match(seq, &cr.Not[i]) {
			return false
		}
	}
	for i := range cr.Or {
		if !c.match(seq, &cr.Or[i][0]) && !c.match(seq, &cr.Or[i][1]) {
			return false
		}
	}
	return true
}

// matchDate compares days, not moments: RFC 3501 has SINCE and BEFORE ignore the time and zone.
func matchDate(t, since, before time.Time) bool {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !before.IsZero() && !t.Before(before) {
		return false
	}
	return true
}

func matchFields(fields gomessage.HeaderFields, pattern string) bool {
	if pattern == "" {
		return fields.Len() > 0
	}
	pattern = strings.ToLower(pattern)
	for fields.Next() {
		v, _ := fields.Text()
		if strings.Contains(strings.ToLower(v), pattern) {
			return true
		}
	}
	return false
}

func matchEntity(e *gomessage.Entity, pattern string, header bool) bool {
	if pattern == "" {
		return true
	}
	if header && matchFields(e.Header.Fields(), pattern) {
		return true
	}
	if mr := e.MultipartReader(); mr != nil {
		for {
			part, err := mr.NextPart()
			if err != nil {
				return false
			}
			if matchEntity(part, pattern, header) {
				return true
			}
		}
	}
	t, _, err := e.Header.ContentType()
	if err != nil || (!strings.HasPrefix(t, "text/") && !strings.HasPrefix(t, "message/")) {
		return false
	}
	buf, err := io.ReadAll(e.Body)
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(buf), bytes.ToLower([]byte(pattern)))
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, flag) {
			return true
		}
	}
	return false
}

func flagStrings(flags []imap.Flag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
	}
	return out
}

func imapFlags(flags []string) []imap.Flag {
	out := make([]imap.Flag, len(flags))
	for i, f := range flags {
		out[i] = imap.Flag(f)
	}
	return out
}

func ioReadFull(r io.Reader, buf []byte) (int, error) { return io.ReadFull(r, buf) }
