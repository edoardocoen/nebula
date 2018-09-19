package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"time"

	"github.com/rs/cors"
	"github.com/samoslab/nebula/client/common"
	"github.com/samoslab/nebula/client/config"
	"github.com/samoslab/nebula/client/daemon"
	regclient "github.com/samoslab/nebula/client/register"
	"github.com/samoslab/nebula/util/aes"
	"github.com/samoslab/nebula/util/filetype"
	"github.com/sirupsen/logrus"
	"github.com/unrolled/secure"
	"golang.org/x/crypto/acme/autocert"
)

const (
	shutdownTimeout = time.Second * 5

	// https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/
	// The timeout configuration is necessary for public servers, or else
	// connections will be used up
	serverReadTimeout  = time.Second * 10
	serverWriteTimeout = time.Second * 60
	serverIdleTimeout  = time.Second * 120

	// Directory where cached SSL certs from Let's Encrypt are stored
	tlsAutoCertCache = "cert-cache"
)

var (
	errInternalServerError = errors.New("Internal Server Error")
)

// HTTPServer exposes the API endpoints and static website
type HTTPServer struct {
	cfg config.Config
	log logrus.FieldLogger
	//log           *logrus.Logger
	cm            *daemon.ClientManager
	httpListener  *http.Server
	httpsListener *http.Server
	quit          chan struct{}
	done          chan struct{}
}

// InitClientManager init client manager
func InitClientManager(log logrus.FieldLogger, webcfg config.Config) (*daemon.ClientManager, error) {
	_, defaultConfig := config.GetConfigFile()
	clientConfig, err := config.LoadConfig(defaultConfig)
	if err != nil {
		if err == config.ErrNoConf {
			return nil, fmt.Errorf("Config file is not ready, please call /store/register to register first")
		} else if err == config.ErrConfVerify {
			return nil, fmt.Errorf("Config file wrong, can not start daemon.")
		}
		log.Errorf("Load config error %v", err)
	}
	cm, err := daemon.NewClientManager(log, webcfg, clientConfig)
	if err != nil {
		log.Infof("New client manager failed %v\n", err)
		return cm, err
	}
	return cm, nil
}

// NewHTTPServer creates an HTTPServer
func NewHTTPServer(log logrus.FieldLogger, cfg config.Config) *HTTPServer {
	cm, err := InitClientManager(log, cfg)
	if err != nil {
		log.Errorf("Init client manager failed, error %v", err)
	}
	return &HTTPServer{
		cfg:  cfg,
		log:  log,
		cm:   cm,
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (s *HTTPServer) GetClientManager() **daemon.ClientManager {
	return &s.cm
}
func (s *HTTPServer) CanBeWork() bool {
	if s.cm != nil {
		return true
	}
	return false
}

// Run runs the HTTPServer
func (s *HTTPServer) Run() error {
	log := s.log
	log.Info("Http service start")
	defer log.Info("Http service closed")
	defer close(s.done)

	var mux http.Handler = s.setupMux()

	allowedHosts := []string{} // empty array means all hosts allowed
	sslHost := ""
	if s.cfg.AutoTLSHost == "" {
		// Note: if AutoTLSHost is not set, but HTTPSAddr is set, then
		// http will redirect to the HTTPSAddr listening IP, which would be
		// either 127.0.0.1 or 0.0.0.0
		// When running behind a DNS name, make sure to set AutoTLSHost
		sslHost = s.cfg.HTTPSAddr
	} else {
		sslHost = s.cfg.AutoTLSHost
		// When using -auto-tls-host,
		// which implies automatic Let's Encrypt SSL cert generation in production,
		// restrict allowed hosts to that host.
		allowedHosts = []string{s.cfg.AutoTLSHost}
	}

	if len(allowedHosts) == 0 {
		log = log.WithField("allowedHosts", "all")
	} else {
		log = log.WithField("allowedHosts", allowedHosts)
	}

	log = log.WithField("sslHost", sslHost)

	secureMiddleware := configureSecureMiddleware(sslHost, allowedHosts)
	mux = secureMiddleware.Handler(mux)

	if s.cfg.HTTPAddr != "" {
		s.httpListener = setupHTTPListener(s.cfg.HTTPAddr, mux)
	}

	handleListenErr := func(f func() error) error {
		if err := f(); err != nil {
			select {
			case <-s.quit:
				return nil
			default:
				log.WithError(err).Error("ListenAndServe or ListenAndServeTLS error")
				return fmt.Errorf("http serve failed: %v", err)
			}
		}
		return nil
	}

	if s.cfg.HTTPAddr != "" {
		log.Info(fmt.Sprintf("HTTP server listening on http://%s", s.cfg.HTTPAddr))
	}
	if s.cfg.HTTPSAddr != "" {
		log.Info(fmt.Sprintf("HTTPS server listening on https://%s", s.cfg.HTTPSAddr))
	}

	var tlsCert, tlsKey string
	if s.cfg.HTTPSAddr != "" {
		log.Info("Using TLS")

		s.httpsListener = setupHTTPListener(s.cfg.HTTPSAddr, mux)

		tlsCert = s.cfg.TLSCert
		tlsKey = s.cfg.TLSKey

		if s.cfg.AutoTLSHost != "" {
			log.Info("Using Let's Encrypt autocert")
			// https://godoc.org/golang.org/x/crypto/acme/autocert
			// https://stackoverflow.com/a/40494806
			certManager := autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(s.cfg.AutoTLSHost),
				Cache:      autocert.DirCache(tlsAutoCertCache),
			}

			s.httpsListener.TLSConfig = &tls.Config{
				GetCertificate: certManager.GetCertificate,
			}

			// These will be autogenerated by the autocert middleware
			tlsCert = ""
			tlsKey = ""
		}

	}

	return handleListenErr(func() error {
		var wg sync.WaitGroup
		errC := make(chan error)

		if s.cfg.HTTPAddr != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.httpListener.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.WithError(err).Println("ListenAndServe error")
					errC <- err
				}
			}()
		}

		if s.cfg.HTTPSAddr != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.httpsListener.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
					log.WithError(err).Error("ListenAndServeTLS error")
					errC <- err
				}
			}()
		}

		done := make(chan struct{})

		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case err := <-errC:
			return err
		case <-s.quit:
			return nil
		case <-done:
			return nil
		}
	})
}

func configureSecureMiddleware(sslHost string, allowedHosts []string) *secure.Secure {
	sslRedirect := true
	if sslHost == "" {
		sslRedirect = false
	}

	return secure.New(secure.Options{
		AllowedHosts: allowedHosts,
		SSLRedirect:  sslRedirect,
		SSLHost:      sslHost,

		// https://developer.mozilla.org/en-US/docs/Web/HTTP/CSP
		// FIXME: Web frontend code has inline styles, CSP doesn't work yet
		// ContentSecurityPolicy: "default-src 'self'",

		// Set HSTS to one year, for this domain only, do not add to chrome preload list
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Strict-Transport-Security
		STSSeconds:           31536000, // 1 year
		STSIncludeSubdomains: false,
		STSPreload:           false,

		// Deny use in iframes
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Frame-Options
		FrameDeny: true,

		// Disable MIME sniffing in browsers
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Content-Type-Options
		ContentTypeNosniff: true,

		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-XSS-Protection
		BrowserXssFilter: true,

		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Referrer-Policy
		// "same-origin" is invalid in chrome
		ReferrerPolicy: "no-referrer",
	})
}

func setupHTTPListener(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}
}

func (s *HTTPServer) setupMux() *http.ServeMux {
	mux := http.NewServeMux()
	handleAPI := func(path string, h http.Handler) {
		// Allow requests from a local samos client
		h = cors.New(cors.Options{
			AllowedOrigins: []string{"http://127.0.0.1:7788"},
		}).Handler(h)

		mux.Handle(path, h)
	}

	// API Methods
	handleAPI("/api/v1/store/register", RegisterHandler(s))
	handleAPI("/api/v1/store/verifyemail", EmailHandler(s))
	handleAPI("/api/v1/store/folder/add", MkfolderHandler(s))
	handleAPI("/api/v1/store/upload", UploadHandler(s))
	handleAPI("/api/v1/store/download", DownloadHandler(s))
	handleAPI("/api/v1/store/list", ListHandler(s))
	handleAPI("/api/v1/store/remove", RemoveHandler(s))
	handleAPI("/api/v1/store/progress", ProgressHandler(s))
	handleAPI("/api/v1/store/uploaddir", UploadDirHandler(s))
	handleAPI("/api/v1/store/downloaddir", DownloadDirHandler(s))
	handleAPI("/api/v1/store/rename", RenameHandler(s))
	handleAPI("/api/v1/task/upload", TaskUploadHandler(s))
	handleAPI("/api/v1/task/uploaddir", TaskUploadDirHandler(s))
	handleAPI("/api/v1/task/download", TaskDownloadHandler(s))
	handleAPI("/api/v1/task/downloaddir", TaskDownloadDirHandler(s))
	handleAPI("/api/v1/task/status", TaskStatusHandler(s))

	handleAPI("/api/v1/package/all", GetAllPackageHandler(s))
	handleAPI("/api/v1/package", GetPackageInfoHandler(s))
	handleAPI("/api/v1/package/buy", BuyPackageHandler(s))
	handleAPI("/api/v1/package/discount", DiscountPackageHandler(s))
	handleAPI("/api/v1/order/all", MyAllOrderHandler(s))
	handleAPI("/api/v1/order/getinfo", GetOrderInfoHandler(s))
	handleAPI("/api/v1/order/recharge/address", RechargeAddressHandler(s))
	handleAPI("/api/v1/order/pay", PayOrderHandler(s))
	handleAPI("/api/v1/order/remove", RemoveOrderHandler(s))
	handleAPI("/api/v1/usage/amount", UsageAmountHandler(s))

	handleAPI("/api/v1/secret/encrypt", EncryFileHandler(s))
	handleAPI("/api/v1/secret/decrypt", DecryFileHandler(s))

	handleAPI("/api/v1/service/status", ServiceStatusHandler(s))
	handleAPI("/api/v1/service/filetype", FileTypeHandler(s))
	handleAPI("/api/v1/service/root", RootPathHandler(s))
	handleAPI("/api/v1/config/import", ConfigImportHandler(s))
	handleAPI("/api/v1/config/export", ConfigExportHandler(s))

	handleAPI("/api/v1/space/verify", SpaceVerifyHandler(s))
	handleAPI("/api/v1/space/password", PasswordHandler(s))
	handleAPI("/api/v1/space/status", SpaceStatusHandler(s))

	// Static files
	mux.Handle("/", http.FileServer(http.Dir(s.cfg.StaticDir)))
	return mux
}

// RegisterReq request struct for register
type RegisterReq struct {
	Email  string `json:"email"`
	Resend bool   `josn:"resend"`
}

// VerifyEmailReq request struct for verify email
type VerifyEmailReq struct {
	Code string `json:"code"`
}

// TaskStatusReq task status request
type TaskStatusReq struct {
	TaskID string `json:"task_id"`
}

// MkfolderReq request struct for make folder
type MkfolderReq struct {
	Folders     []string `json:"folders"`
	Parent      string   `json:"parent"`
	Interactive bool     `json:"interactive"`
	Sno         uint32   `json:"space_no"`
}

// RenameReq request struct for move file, src is source file id which get by list
type RenameReq struct {
	Source string `json:"src"`
	Dest   string `json:"dest"`
	IsPath bool   `json:"ispath"`
	Sno    uint32 `json:"space_no"`
}

// ListReq request struct for list files
type ListReq struct {
	Path     string `json:"path"`
	PageSize uint32 `json:"pagesize"`
	PageNum  uint32 `json:"pagenum"`
	SortType string `json:"sorttype"`
	AscOrder bool   `json:"ascorder"`
	Sno      uint32 `json:"space_no"`
}

// RemoveReq request struct for remove file
type RemoveReq struct {
	Target    string `json:"target"`
	Recursion bool   `json:"recursion"`
	IsPath    bool   `json:"ispath"`
	Sno       uint32 `json:"space_no"`
}

// ProgressReq request struct for progress bar
type ProgressReq struct {
	Files []string `json:"files"`
}

// ProgressRsp response for progress bar
type ProgressRsp struct {
	Progress map[string]float64 `json:"progress"`
}

// EncryFileReq encrypt file request
type EncryFileReq struct {
	FileName   string `json:"file"`
	Password   string `json:"password"`
	OutputFile string `json:output_file`
}

// DecryFileReq decrypt file request
type DecryFileReq struct {
	FileName   string `json:"file"`
	Password   string `json:"password"`
	OutputFile string `json:output_file`
}

// ServiceStatus service status request
type ServiceStatus struct {
	Status bool `json:"status"`
}

// RootPath root path
type RootPath struct {
	Root string `json:"root"`
}

// PasswordReq set password for privacy space
type PasswordReq struct {
	Password string `json:"password"`
	SpacoNo  uint32 `json:"space_no"`
}

// SpaceStatusReq space status
type SpaceStatusReq struct {
	SpacoNo uint32 `json:"space_no"`
}

// ConfigImportReq import config
type ConfigImportReq struct {
	FileName string `json:"filename"`
}

// ConfigExportReq export config
type ConfigExportReq struct {
	Filename string `json:"filename"`
}

// ServiceStatusHandler returns service status
func ServiceStatusHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		if !validMethod(ctx, w, r, []string{http.MethodGet}) {
			return
		}

		ss := ServiceStatus{
			Status: s.CanBeWork(),
		}

		if err := JSONResponse(w, ss); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// RootPathHandler set user root path handler
func RootPathHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &RootPath{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.Root == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument root must not empty"))
			return
		}

		err := s.cm.SetRoot(req.Root)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// PasswordHandler set user root path handler
func PasswordHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &PasswordReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.Password == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument password must not empty"))
			return
		}

		err := s.cm.SetPassword(req.SpacoNo, req.Password)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// SpaceVerifyHandler check password correctness
func SpaceVerifyHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &PasswordReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.Password == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument password must not empty"))
			return
		}

		err := s.cm.VerifyPassword(req.SpacoNo, req.Password)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// SpaceStatusHandler check password correctness
func SpaceStatusHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &SpaceStatusReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		result, code, errmsg := "ok", 0, ""
		_, err := s.cm.OM.UsageAmount()
		if err != nil {
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		} else {
			err := s.cm.CheckSpaceStatus(req.SpacoNo)
			if err != nil {
				result = ""
				code, errmsg = common.StatusErrFromError(err)
			}
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// ConfigImportHandler set user root path handler
func ConfigImportHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := s.log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &ConfigImportReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.FileName == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument filename must not empty"))
			return
		}

		err := config.ImportConfig(req.FileName, s.cfg.ConfigFile)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		} else {
			if s.cm == nil {
				cm, err := InitClientManager(log, s.cfg)
				if err != nil {
					code = 1
					errmsg = err.Error()
				} else {
					s.cm = cm
				}
			}
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// ConfigExportHandler set user root path handler
func ConfigExportHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodGet}) {
			return
		}

		defer r.Body.Close()

		confFile := s.cm.ExportFile()
		w.Header().Set("Content-Disposition", "attachment;filename=config.json")
		http.ServeFile(w, r, confFile)
	}
}

// RegisterHandler client register handler, client must register first then can be using service
func RegisterHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log := s.log
		ctx := r.Context()
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		regReq := &RegisterReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&regReq); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if regReq.Email == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument email must not empty"))
			return
		}

		var err error
		if regReq.Resend {
			err = regclient.ResendVerifyCode(s.cfg.ConfigFile, s.cfg.TrackerServer)
		} else {
			log.Infof("Register email %s dir %s", regReq.Email, s.cfg.ConfigFile)
			err = regclient.RegisterClient(log, s.cfg.ConfigFile, s.cfg.TrackerServer, regReq.Email)
		}
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Send email %+v error %v", regReq, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		} else {
			if !regReq.Resend && s.cm == nil {
				cm, err := InitClientManager(log, s.cfg)
				if err != nil {
					code = 1
					errmsg = err.Error()
				} else {
					s.cm = cm
				}
			}
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// EmailHandler email verified handler
func EmailHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		mailReq := &VerifyEmailReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&mailReq); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if mailReq.Code == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument code must not empty"))
			return
		}

		err := regclient.VerifyEmail(s.cfg.ConfigFile, s.cfg.TrackerServer, mailReq.Code)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Verify email %+v error %v", mailReq, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// MkfolderHandler create folders
// Method: POST
// Accept: application/json
// URI: /store/folder/add
// Args:
func MkfolderHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		mkReq := &MkfolderReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&mkReq); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		defer r.Body.Close()
		if mkReq.Parent == "" || len(mkReq.Folders) == 0 {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument parent or folders must not empty"))
			return
		}
		for _, folder := range mkReq.Folders {
			if strings.Contains(folder, "/") {
				errorResponse(ctx, w, http.StatusBadRequest, fmt.Errorf("folder %s contains /", folder))
				return
			}
		}

		log.Infof("Mkfolder parent %s folders %+v", mkReq.Parent, mkReq.Folders)
		code, errmsg := 0, ""
		result, err := s.cm.MkFolder(mkReq.Parent, mkReq.Folders, mkReq.Interactive, mkReq.Sno)
		if err != nil {
			log.Errorf("Create folder %+v error %v", mkReq.Parent, mkReq.Folders, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// UploadHandler upload file handler
func UploadHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.UploadReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Filename == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument filename or dest_dir must not empty"))
			return
		}

		log.Infof("Upload files %+v", req.Filename)
		err := s.cm.UploadFile(req.Filename, req.Dest, req.Interactive, req.NewVersion, req.IsEncrypt, req.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Upload %+v error %v", req, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// TaskUploadHandler upload file handler
func TaskUploadHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.UploadReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Filename == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument filename or dest_dir must not empty"))
			return
		}

		log.Infof("Upload files %+v", req.Filename)
		result, err := s.cm.AddTask(common.TaskUploadFileType, req)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("Upload %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// TaskUploadDirHandler upload directory handler
func TaskUploadDirHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.UploadDirReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Parent == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument parent or dest_dir must not empty"))
			return
		}

		log.Infof("Upload parent %s", req.Parent)
		result, err := s.cm.AddTask(common.TaskUploadDirType, req)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("Upload %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// TaskDownloadHandler download file handler
func TaskDownloadHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.DownloadReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.FileHash == "" || req.Dest == "" || req.FileSize == 0 || req.FileName == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument filehash, dest_dir, filesize or filename must not empty"))
			return
		}

		log.Infof("Download  %+v", req)
		result, err := s.cm.AddTask(common.TaskDownloadFileType, req)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("Download files %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// TaskDownloadDirHandler download directory from provider
func TaskDownloadDirHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.DownloadDirReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Parent == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument parent and dest_dir  must not empty"))
			return
		}

		log.Infof("downloaddir request %+v", req)
		result, err := s.cm.AddTask(common.TaskDownloadDirType, req)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("Download dir %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// TaskStatusHandler upload directory handler
func TaskStatusHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &TaskStatusReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.TaskID == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument task_id must not empty"))
			return
		}

		log.Infof("task id %s", req.TaskID)
		result, err := s.cm.TaskStatus(req.TaskID)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("Upload %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// UploadDirHandler upload directory handler
func UploadDirHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.UploadDirReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Parent == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument parent or dest_dir must not empty"))
			return
		}

		log.Infof("Upload parent %s", req.Parent)
		err := s.cm.UploadDir(req.Parent, req.Dest, req.Interactive, req.NewVersion, req.IsEncrypt, req.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Upload %+v error %v", req, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// DownloadHandler download file handler
func DownloadHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.DownloadReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.FileHash == "" || req.Dest == "" || req.FileSize == 0 || req.FileName == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument filehash, dest_dir, filesize or filename must not empty"))
			return
		}

		log.Infof("Download  %+v", req)
		err := s.cm.DownloadFile(req.FileName, req.Dest, req.FileHash, req.FileSize, req.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Download files %+v error %v", req, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// DownloadDirHandler download directory from provider
func DownloadDirHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &common.DownloadDirReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Parent == "" || req.Dest == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument parent and dest_dir  must not empty"))
			return
		}

		log.Infof("downloaddir request %+v", req)
		err := s.cm.DownloadDir(req.Parent, req.Dest, req.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Download dir %+v error %v", req, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// RenameHandler rename file handler
func RenameHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &RenameReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		if req.Dest == "" || req.Source == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument dest or src must not empty"))
			return
		}

		log.Infof("Move request %+v", req)
		err := s.cm.MoveFile(req.Source, req.Dest, req.IsPath, req.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Move file %+v error %v", req, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// ListHandler list files handler
func ListHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &ListReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.Path == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument path must not empty"))
			return
		}

		log.Infof("List %+v", req)
		result, err := s.cm.ListFiles(req.Path, req.PageSize, req.PageNum, req.SortType, req.AscOrder, req.Sno)
		code, errmsg := 0, ""
		if err != nil {
			log.Errorf("List file %+v error %v", req, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// RemoveHandler remove file handler
func RemoveHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		rmReq := &RemoveReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&rmReq); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if rmReq.Target == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument target must not empty"))
			return
		}

		log.Infof("Remove %+v", rmReq)
		err := s.cm.RemoveFile(rmReq.Target, rmReq.Recursion, rmReq.IsPath, rmReq.Sno)
		result, code, errmsg := "ok", 0, ""
		if err != nil {
			log.Errorf("Remove files %+v error %v", rmReq, err)
			result = ""
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// ProgressHandler progress bar handler
func ProgressHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		progressReq := &ProgressReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&progressReq); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()

		log.Infof("Progress %+v", progressReq)
		progressRsp, err := s.cm.GetProgress(progressReq.Files)
		code := 0
		errmsg := ""
		if err != nil {
			log.Errorf("Progress files %+v error %v", progressReq, err)
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, progressRsp, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// EncryFileHandler encrypt file handler
func EncryFileHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &EncryFileReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.FileName == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument file must not empty"))
			return
		}
		if req.Password == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument password must not empty"))
			return
		}
		if len(req.Password) != 16 {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("password lenght must 16"))
			return
		}

		log.Infof("Encrypt file %+v\n", req.FileName)
		if req.OutputFile == "" {
			req.OutputFile = req.FileName
		}
		err := aes.EncryptFile(req.FileName, []byte(req.Password), req.OutputFile)
		code := 0
		errmsg := ""
		result := true
		if err != nil {
			log.Errorf("Encrypt file %+v error %v", req.FileName, err)
			result = false
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// DecryFileHandler decrypt file handler
func DecryFileHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.cm.Log
		w.Header().Set("Accept", "application/json")

		if !validMethod(ctx, w, r, []string{http.MethodPost}) {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			errorResponse(ctx, w, http.StatusUnsupportedMediaType, errors.New("Invalid content type"))
			return
		}

		req := &DecryFileReq{}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			err = fmt.Errorf("Invalid json request body: %v", err)
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}

		defer r.Body.Close()
		if req.FileName == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument file must not empty"))
			return
		}
		if req.Password == "" {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("argument password must not empty"))
			return
		}
		if len(req.Password) != 16 {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("password lenght must 16"))
			return
		}

		log.Infof("encrypt file %+v\n", req.FileName)
		if req.OutputFile == "" {
			req.OutputFile = req.FileName
		}
		err := aes.DecryptFile(req.FileName, []byte(req.Password), req.OutputFile)
		code := 0
		errmsg := ""
		result := true
		if err != nil {
			log.Errorf("Decrypt file %+v error %v", req.FileName, err)
			result = false
			code, errmsg = common.StatusErrFromError(err)
		}

		rsp, err := common.MakeUnifiedHTTPResponse(code, result, errmsg)
		if err != nil {
			errorResponse(ctx, w, http.StatusBadRequest, err)
			return
		}
		if err := JSONResponse(w, rsp); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// FileTypeHandler returns service status
func FileTypeHandler(s *HTTPServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.CanBeWork() {
			errorResponse(ctx, w, http.StatusBadRequest, errors.New("register first"))
			return
		}
		log := s.log
		if !validMethod(ctx, w, r, []string{http.MethodGet}) {
			return
		}

		types := filetype.SupportTypes()

		if err := JSONResponse(w, types); err != nil {
			log.Infof("Error %v\n", err)
		}
	}
}

// JSONResponse marshal data into json and write response
func JSONResponse(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	d, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return err
	}

	_, err = w.Write(d)
	return err
}

func errorResponse(ctx context.Context, w http.ResponseWriter, code int, err error) {
	unifiedres, err := common.MakeUnifiedHTTPResponse(code, "", err.Error())
	if err != nil {
		return
	}
	if err := JSONResponse(w, unifiedres); err != nil {
		fmt.Printf("Error response failed")
	}
}

// Shutdown stops the HTTPServer
func (s *HTTPServer) Shutdown() {
	log := s.log
	log.Info("Shutting down HTTP server(s)")
	defer log.Info("Shutdown HTTP server(s)")
	close(s.quit)

	var wg sync.WaitGroup
	wg.Add(2)

	shutdown := func(proto string, ln *http.Server) {
		defer wg.Done()
		if ln == nil {
			return
		}
		log := log.WithFields(logrus.Fields{
			"proto":   proto,
			"timeout": shutdownTimeout,
		})

		defer log.Info("Shutdown server")
		log.Info("Shutting down server")

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := ln.Shutdown(ctx); err != nil {
			log.WithError(err).Error("HTTP server shutdown error")
		}
	}

	shutdown("HTTP", s.httpListener)
	shutdown("HTTPS", s.httpsListener)

	if s.cm != nil {
		s.cm.Shutdown()
	}
	wg.Wait()

	<-s.done
}

func validMethod(ctx context.Context, w http.ResponseWriter, r *http.Request, allowed []string) bool {
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}

	w.Header().Set("Allow", strings.Join(allowed, ", "))

	status := http.StatusMethodNotAllowed
	errorResponse(ctx, w, status, errors.New("Invalid request method"))

	return false
}
