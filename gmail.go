package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// BodyType - what type of body to set
type BodyType string

// BodyType values
const (
	Auto BodyType = "auto"
	HTML BodyType = "html"
	Text BodyType = "text"
)

// GoogleMessage describes an email message.
type GoogleMessage struct {
	Header textproto.MIMEHeader   // headers
	parts  map[string]*googlePart // the list of file by names

	AccessToken string
}

// Token - implement TokenSource interface
func (m *GoogleMessage) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: m.AccessToken,
	}, nil
}

const _body = "\000body"         // the file name with the contents of the message
const _bodyTEXT = "\000bodyTEXT" // the file name with the contents of the message
const _bodyHTML = "\000bodyHTML" // the file name with the contents of the message

// Attach attaches to the message an attachment as a file. Passing an empty
// content deletes the file with the same name if it was previously added.
func (m *GoogleMessage) Attach(name string, data []byte, headers *textproto.MIMEHeader) error {
	if len(data) == 0 {
		if m.parts != nil {
			delete(m.parts, name)
		}
		return nil
	}

	name = filepath.Base(name)
	switch name {
	case ".", "..", string(filepath.Separator):
		return fmt.Errorf("bad file name: %v", name)
	}

	var h = make(textproto.MIMEHeader)
	if headers != nil {
		h = *headers
	}

	var contentType = h.Get("Content-Type")

	if contentType == "" {
		if name == _bodyHTML {
			contentType = "text/html"
		} else {
			contentType = mime.TypeByExtension(filepath.Ext(name))
		}

		if contentType == "" {
			contentType = http.DetectContentType(data)
		}

		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
	}

	var coding = h.Get("Content-Transfer-Encoding")

	if coding == "" || !((coding == "quoted-printable") || (coding == "base64")) {
		coding = "quoted-printable"
		if !strings.HasPrefix(contentType, "text") {
			if name == _body || name == _bodyHTML || name == _bodyTEXT {
				return fmt.Errorf("unsupported body content type: %v", contentType)
			}
			coding = "base64"
		}

		h.Set("Content-Transfer-Encoding", coding)
	}

	if name != _body && name != _bodyHTML && name != _bodyTEXT {
		disposition := h.Get("Content-Disposition")
		if disposition == "" {
			disposition = fmt.Sprintf("attachment; filename=%s", name)
			h.Set("Content-Disposition", disposition)
		}
	}

	if m.parts == nil {
		m.parts = make(map[string]*googlePart)
	}

	m.parts[name] = &googlePart{
		header: h,
		data:   data,
	}

	return nil
}

// SetBody sets the contents of the text of the letter.
//
// You can use text or HTML message format (is determined automatically). To
// guarantee that the format will be specified as HTML, consider wrapping the
// text with <html> tag. When adding the HTML content, text version, to support
// legacy mail program will be added automatically. When you try to add as
// message binary data will return an error. You can pass as a parameter the nil,
// then the message will be without a text submission. Use bodyType to specify
// which type of body is being set.  Using Auto only allows one body part.  To have both
// an html body and a text body, call twice, once for each kind
func (m *GoogleMessage) SetBody(data []byte, headers *textproto.MIMEHeader, bodyType BodyType) error {
	name := _body
	switch bodyType {
	case HTML:
		name = _bodyHTML
	case Text:
		name = _bodyTEXT
	case Auto:
		fallthrough
	default:
		name = _body
	}
	return m.Attach(name, data, headers)
}

// Has returns true if a file with that name was in the message as an attachment.
func (m *GoogleMessage) Has(name string) bool {
	_, ok := m.parts[name]
	return ok
}

// WriteTo generates and writes the text representation of mail messages.
func (m *GoogleMessage) WriteTo(w io.Writer) (int64, error) {
	var numBytes int64

	if len(m.parts) == 0 {
		return 0, errors.New("contents are undefined")
	}

	var headers = make(textproto.MIMEHeader)
	if m.Header.Get("MIME-Version") == "" {
		headers.Set("MIME-Version", "1.0")
	}

	// copy the primary header of the message
	for k, v := range m.Header {
		for _, v2 := range v {
			headers.Add(k, v2)
		}
	}

	// check that only defined the basic message, no attachments
	if len(m.parts) == 1 && m.Has(_body) {
		body := m.parts[_body]
		for k, v := range body.header {
			for _, v2 := range v {
				headers.Add(k, v2)
			}
		}

		numBytes += int64(writeEmailHeaders(w, headers))

		if bytesWritten, err := body.writeGoogleData(w); err != nil {
			return numBytes + int64(bytesWritten), err
		}
		return numBytes, nil
	}

	// there are attached files
	var mw = multipart.NewWriter(w)
	defer mw.Close()
	headers.Set("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%s", mw.Boundary()))

	writeEmailHeaders(w, headers)

	for _, p := range m.parts {
		pw, err := mw.CreatePart(p.header)
		if err != nil {
			return numBytes, err
		}

		if bytesWritten, err := p.writeGoogleData(pw); err != nil {
			return numBytes + int64(bytesWritten), err
		}
	}
	return numBytes, nil
}

// Send sends the message through GMail.
// Returns the Message-Id header for the sent email
func (m *GoogleMessage) Send() (string, error) {
	var buf bytes.Buffer
	m.WriteTo(&buf)

	body := base64.RawURLEncoding.EncodeToString(buf.Bytes())

	var gmailMessage = &gmail.Message{Raw: body}

	srv, err := gmail.NewService(context.Background(), option.WithTokenSource(m), option.WithUserAgent("XANTDev/gmail-go"))

	resp, err := srv.Users.Messages.Send("me", gmailMessage).Do()
	if err != nil {
		return "", err
	}

	sentMsg, err := srv.Users.Messages.Get("me", resp.Id).Do()
	if err != nil {
		return "", err
	}

	var messageID string
	if sentMsg.Payload != nil {
		if sentMsg.Payload.Headers != nil {
			for _, v := range sentMsg.Payload.Headers {
				if v.Name == "Message-Id" {
					messageID = v.Value
					break
				}
			}
		}
	}

	return messageID, nil
}

// googlePart describes googlePart email message: the file or message.
type googlePart struct {
	header textproto.MIMEHeader // headers
	data   []byte               // content
}

// writeGoogleData writes the contents of the message file with maintain the coding
// system. At the moment only implemented quoted-printable and base64 encoding.
// For all others, an error is returned.
func (p *googlePart) writeGoogleData(w io.Writer) (numBytes int, err error) {
	switch name := p.header.Get("Content-Transfer-Encoding"); name {
	case "quoted-printable":
		enc := quotedprintable.NewWriter(w)
		numBytes, err = enc.Write(p.data)
		enc.Close()
	case "base64":
		enc := base64.NewEncoder(base64.StdEncoding, w)
		numBytes, err = enc.Write(p.data)
		enc.Close()
	default:
		err = fmt.Errorf("unsupported transform encoding: %v", name)
	}
	return numBytes, err
}

// writeEmailHeaders writes the header of the message or file.
func writeEmailHeaders(w io.Writer, h textproto.MIMEHeader) (numBytes int) {
	var keys = make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}

	for _, k := range keys {
		numBytes += writeHeader(w, k, h[k]...)
	}
	fmt.Fprintf(w, "\r\n") // add the offset from the header

	return
}

func writeHeader(w io.Writer, k string, v ...string) (numBytes int) {
	bytesWritten, _ := io.WriteString(w, k)
	numBytes += bytesWritten
	if len(v) == 0 {
		bytesWritten, _ := io.WriteString(w, ":\r\n")
		numBytes += bytesWritten
		return numBytes
	}
	bytesWritten, _ = io.WriteString(w, ": ")
	numBytes += bytesWritten

	// Max header line length is 78 characters in RFC 5322 and 76 characters
	// in RFC 2047. So for the sake of simplicity we use the 76 characters
	// limit.
	charsLeft := 76 - len(k) - len(": ")

	for i, s := range v {
		// If the line is already too long, insert a newline right away.
		if charsLeft < 1 {
			if i == 0 {
				bytesWritten, _ := io.WriteString(w, "\r\n ")
				numBytes += bytesWritten
			} else {
				bytesWritten, _ := io.WriteString(w, ",\r\n ")
				numBytes += bytesWritten
			}
			charsLeft = 75
		} else if i != 0 {
			bytesWritten, _ := io.WriteString(w, ", ")
			numBytes += bytesWritten
			charsLeft -= 2
		}

		// While the header content is too long, fold it by inserting a newline.
		for len(s) > charsLeft {
			bytesWritten, s = writeLine(w, s, charsLeft)
			numBytes += bytesWritten
			charsLeft = 75
		}
		bytesWritten, _ = io.WriteString(w, s)
		numBytes += bytesWritten
		if i := strings.LastIndexByte(s, '\n'); i != -1 {
			charsLeft = 75 - (len(s) - i - 1)
		} else {
			charsLeft -= len(s)
		}
	}
	bytesWritten, _ = io.WriteString(w, "\r\n")
	numBytes += bytesWritten
	return numBytes
}

func writeLine(w io.Writer, s string, charsLeft int) (int, string) {
	var numBytes int

	// If there is already a newline before the limit. Write the line.
	if i := strings.IndexByte(s, '\n'); i != -1 && i < charsLeft {
		bytesWritten, _ := io.WriteString(w, s[:i+1])
		numBytes += bytesWritten
		return numBytes, s[i+1:]
	}

	for i := charsLeft - 1; i >= 0; i-- {
		if s[i] == ' ' {
			bytesWritten, _ := io.WriteString(w, s[:i])
			numBytes += bytesWritten
			bytesWritten, _ = io.WriteString(w, "\r\n ")
			numBytes += bytesWritten
			return numBytes, s[i+1:]
		}
	}

	// We could not insert a newline cleanly so look for a space or a newline
	// even if it is after the limit.
	for i := 75; i < len(s); i++ {
		if s[i] == ' ' {
			bytesWritten, _ := io.WriteString(w, s[:i])
			numBytes += bytesWritten
			bytesWritten, _ = io.WriteString(w, "\r\n ")
			numBytes += bytesWritten
			return numBytes, s[i+1:]
		}
		if s[i] == '\n' {
			bytesWritten, _ := io.WriteString(w, s[:i+1])
			numBytes += bytesWritten
			return bytesWritten, s[i+1:]
		}
	}

	// Too bad, no space or newline in the whole string. Just write everything.
	bytesWritten, _ := io.WriteString(w, s)
	numBytes += bytesWritten
	return numBytes, ""
}
