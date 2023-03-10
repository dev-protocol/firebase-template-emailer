package emailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"os"

	"net/http"
	"net/mail"
	"net/url"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"

	"github.com/go-chi/render"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/sendgrid/sendgrid-go"
	sgMail "github.com/sendgrid/sendgrid-go/helpers/mail"
)

// SendEmailRequest is the request payload for sending an email.
type SendEmailRequest struct {
	Email *string `json:"email"`

	// this should be like "/authentication" or "/authentication/<site-name>"
	// since the base url is set in .env
	SubDomain *string `json:"subDomain,omitempty"`
}

func (emailRequest *SendEmailRequest) Bind(r *http.Request) error {
	// emailRequest.Email is nil if no Email field is sent in the request. Return an
	// error to avoid a nil pointer dereference.
	if emailRequest.Email == nil {
		return errors.New("missing required Email field")
	}

	// Ensure email is valid
	_, err := mail.ParseAddress(*emailRequest.Email)
	if err != nil {
		return errors.New("invalid Email Address")
	}

	return nil
}

type ErrResponse struct {
	Err            error `json:"-"` // low-level runtime error
	HTTPStatusCode int   `json:"-"` // http response status code

	StatusText string `json:"status"`          // user-level status message
	AppCode    int64  `json:"code,omitempty"`  // application-specific error code
	ErrorText  string `json:"error,omitempty"` // application-level error message, for debugging
}

func (e *ErrResponse) Render(w http.ResponseWriter, r *http.Request) error {
	render.Status(r, e.HTTPStatusCode)
	return nil
}

func ErrInvalidRequest(err error) render.Renderer {
	return &ErrResponse{
		Err:            err,
		HTTPStatusCode: 400,
		StatusText:     "Invalid request.",
		ErrorText:      err.Error(),
	}
}

func SendEmail(w http.ResponseWriter, r *http.Request) {

	// Enable CORS
	// Set CORS headers for the preflight request
	// This is so we don't have errors testing locally
	// We should make this dynamic and launch prod and dev versions
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Set CORS headers for the main request.
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := context.Background()

	// parse data from request
	data := &SendEmailRequest{}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	config := &firebase.Config{
		ProjectID: os.Getenv("FIREBASE_PROJECT_ID"),
	}

	// initialize firebase
	app, err := firebase.NewApp(ctx, config)
	if err != nil {
		log.Println("error creating new firebase app")
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// initialize firebase auth client
	client, err := app.Auth(ctx)
	if err != nil {
		log.Println("error creating new firebase auth client")
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// Ensure RedirectUrl is a valid URL

	callbackUrlBase, err := url.ParseRequestURI(os.Getenv("FIREBASE_CALLBACK_URL"))
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
	}

	var callbackUrl *url.URL
	var isSignIn bool

	/**
	 * Subdomain exists means verify
	 */
	if data.SubDomain != nil {
		siteName := *data.SubDomain
		callbackUrl = callbackUrlBase.JoinPath(siteName)
		isSignIn = false

		/**
		 * Subdomain does not exist means sign in
		 */
	} else {
		callbackUrl = callbackUrlBase
		isSignIn = true
	}

	emailTemplate, err := template.ParseFiles("email_template.html")
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// callbackUrl := callbackUrlBase.JoinPath(*data.RedirectPath)
	actionCodeSettings := newActionCodeSettings(callbackUrl.String())

	link, err := client.EmailSignInLink(ctx, *data.Email, actionCodeSettings)
	if err != nil {
		log.Println("error creating new firebase link")
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	templateVars := struct {
		EmailVerificationLink string
		IsSignIn              bool
	}{
		EmailVerificationLink: link,
		IsSignIn:              isSignIn,
	}

	// write templateVars to template instance
	var templateInstance bytes.Buffer
	err = emailTemplate.Execute(&templateInstance, templateVars)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	fromEmail := os.Getenv("SENDGRID_FROM_EMAIL")
	_, err = mail.ParseAddress(fromEmail)
	if err != nil {
		log.Println("error parsing address")
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	from := sgMail.NewEmail(os.Getenv("SENDGRID_FROM_NAME"), fromEmail)
	to := sgMail.NewEmail(*data.Email, *data.Email)
	plainTextContent := fmt.Sprintf("Welcome to Clubs! Follow the link to authenticate: %s.", link)
	message := sgMail.NewSingleEmail(from, os.Getenv("SENDGRID_EMAIL_SUBJECT"), to, plainTextContent, templateInstance.String())
	sgClient := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	sendResponse, err := sgClient.Send(message)
	if err != nil {
		log.Println("error in sendResponse!!!")
		log.Println(err)
	} else {
		fmt.Println(sendResponse.StatusCode)
		fmt.Println(sendResponse.Body)
		fmt.Println(sendResponse.Headers)
	}

	res, err := json.Marshal(sendResponse)
	if err != nil {
		log.Println("error in json marshal")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(res)
}

func newActionCodeSettings(redirectUrl string) *auth.ActionCodeSettings {
	actionCodeSettings := &auth.ActionCodeSettings{
		URL: redirectUrl,
	}
	return actionCodeSettings
}

func init() {
	fixDir()
	// register http function
	functions.HTTP("SendEmail", SendEmail)
}
