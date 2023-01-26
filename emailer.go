package emailer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"

	"net/http"
	"net/mail"
	"net/url"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"github.com/joho/godotenv"

	"github.com/sendgrid/sendgrid-go"
	sgMail "github.com/sendgrid/sendgrid-go/helpers/mail"
)

// SendEmailRequest is the request payload for sending an email.
type SendEmailRequest struct {
	Email *string `json:"email"`
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

func newActionCodeSettings(redirectUrl string) *auth.ActionCodeSettings {
	// [START init_action_code_settings]
	actionCodeSettings := &auth.ActionCodeSettings{
		URL:                   redirectUrl,
		HandleCodeInApp:       true,
		IOSBundleID:           "com.example.ios",
		AndroidPackageName:    "com.example.android",
		AndroidInstallApp:     true,
		AndroidMinimumVersion: "12",
		DynamicLinkDomain:     "coolapp.page.link",
	}
	// [END init_action_code_settings]
	return actionCodeSettings
}

func sendEmail(w http.ResponseWriter, r *http.Request) {

	// parse data from request
	data := &SendEmailRequest{}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	var myEnv map[string]string
	myEnv, err := godotenv.Read()
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	config := &firebase.Config{
		ProjectID: myEnv["FIREBASE_PROJECT_ID"],
	}

	// initialize firebase
	app, err := firebase.NewApp(context.Background(), config)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// initialize firebase auth client
	client, err := app.Auth(context.Background())
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// Ensure RedirectUrl is a valid URL
	callbackUrl, err := url.ParseRequestURI(myEnv["FIREBASE_CALLBACK_URL"])
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	actionCodeSettings := newActionCodeSettings(callbackUrl.String())

	link, err := client.EmailVerificationLinkWithSettings(context.Background(), *data.Email, actionCodeSettings)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	emailTemplate, err := template.ParseFiles("email_template.html")
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	templateVars := struct {
		EmailVerificationLink string
	}{
		EmailVerificationLink: link,
	}

	// write templateVars to template instance
	var templateInstance bytes.Buffer
	err = emailTemplate.Execute(&templateInstance, templateVars)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	fromEmail := myEnv["SENDGRID_FROM_EMAIL"]
	_, err = mail.ParseAddress(fromEmail)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	from := sgMail.NewEmail(myEnv["SENDGRID_FROM_NAME"], fromEmail)
	to := sgMail.NewEmail(*data.Email, *data.Email)
	plainTextContent := fmt.Sprintf("Welcome to Clubs! Follow the link to authenticate: %s.", link)
	message := sgMail.NewSingleEmail(from, myEnv["SENDGRID_EMAIL_SUBJECT"], to, plainTextContent, templateInstance.String())
	sgClient := sendgrid.NewSendClient(myEnv["SENDGRID_API_KEY"])
	response, err := sgClient.Send(message)
	if err != nil {
		log.Println(err)
	} else {
		fmt.Println(response.StatusCode)
		fmt.Println(response.Body)
		fmt.Println(response.Headers)
	}
}

func init() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Post("/", sendEmail)
	http.ListenAndServe(":3000", r)
}
