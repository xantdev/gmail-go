package example

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/xantdev/gmail-go"
)

// Email - information for the email to be sent
type Email struct {
	To          EmailAddresses
	Cc          EmailAddresses
	Bcc         EmailAddresses
	From        EmailAddress
	Subject     string
	Body        string
	Attachments []AttachmentData
	InReplyTo   string // Message-id header of messaged being replied to
	References  []string
}

// EmailAddresses - alias for []EmailAddress
type EmailAddresses []EmailAddress

// Addresses - returns a slice of just the address portion of EmailAddresses
func (ea EmailAddresses) Addresses() []string {
	addrs := []string{}
	for _, emailAddress := range ea {
		addrs = append(addrs, emailAddress.Address)
	}

	return addrs
}

// ArrList - convert to comma-seperated list of email Addresses
func (ea EmailAddresses) ArrList(includeName bool) string {
	if includeName == false {
		arr := ea.Addresses()

		if len(arr) == 1 {
			return arr[0]
		}
		return strings.Join(arr, ",")
	}

	arr := []string{}
	for _, emailAddress := range ea {
		addr := mail.Address{Name: emailAddress.Name, Address: emailAddress.Address}
		arr = append(arr, addr.String())
	}

	if len(arr) == 1 {
		return arr[0]
	}

	return strings.Join(arr, ",")
}

// EmailAddress struct
type EmailAddress struct {
	Name    string // display name
	Address string // email address
}

//AttachmentData struct
type AttachmentData struct {
	EncodedAttachment string
	FileName          string
}

func newGoogleMessage(email Email, customHeaders map[string]string, token string) (*gmail.GoogleMessage, error) {
	var header = make(textproto.MIMEHeader)

	header.Set("From", email.From.Address)
	header.Set("Reply-To", email.From.Address)

	if len(email.To) > 0 {
		header.Set("To", email.To.ArrList(true))
	}

	if len(email.Cc) > 0 {
		header.Set("cc", email.Cc.ArrList(true)) // case here matters - anything but cc results in a corrupt header :(
	}

	if len(email.Bcc) > 0 {
		header.Set("bcc", email.Bcc.ArrList(true)) // case here matters - anything but bcc results in a corrupt header :(
	}

	if email.Subject != "" {
		header.Set("Subject", mime.QEncoding.Encode("utf-8", email.Subject))
	}

	if email.InReplyTo != "" && len(email.References) > 0 {
		header.Set("InReplyTo", email.InReplyTo)
		references := strings.Join(email.References, ",")
		header.Set("References", references)
	}

	for k, v := range customHeaders {
		header.Set(k, v)
	}

	var msg = &gmail.GoogleMessage{
		Header:      header,
		AccessToken: token,
	}

	for _, attachment := range email.Attachments {
		content, err := base64.StdEncoding.DecodeString(attachment.EncodedAttachment)
		if err != nil {
			return nil, err
		}

		if err := msg.Attach(attachment.FileName, content, nil); err != nil {
			return nil, err
		}
	}

	if len(email.Body) > 0 {
		if err := msg.SetBody([]byte(email.Body), nil, gmail.Auto); err != nil {
			return msg, err
		}
	}
	return msg, nil
}

func main() {
	token := "123456abdcef" // get a valid access token from gmail
	email := Email{
		To:          EmailAddresses{EmailAddress{Address: "to@address", Name: "To recipient"}},
		Cc:          EmailAddresses{EmailAddress{Address: "cc@address", Name: "Cc recipient"}},
		Bcc:         EmailAddresses{EmailAddress{Address: "bcc@address", Name: "Bcc recipient"}},
		Subject:     "The subject of the email",
		From:        EmailAddress{Address: "sender@address", Name: "Sender"},
		Body:        "<html><body><p>This is a paragraph</p></body></html>",
		Attachments: []AttachmentData{AttachmentData{EncodedAttachment: "Base64Encoded data", FileName: "TheNameOfTheFile.txt"}},
		InReplyTo:   "TheMessageId header for the email being replied to",
		References:  []string{"The", "MessageId", "headers", "for", "the", "conversation"},
	}
	customHeaders := map[string]string{
		"X-Custom-Header": "header value",
	}

	msg, err := newGoogleMessage(email, customHeaders, token)
	if err != nil {
		panic(err)
	}

	messageID, err := msg.Send(nil)
	if err != nil {
		panic(err)
	}

	fmt.Println("Message-Id: ", messageID)
}
