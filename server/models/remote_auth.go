package models

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meshery/meshkit/logger"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

const (
	// Stores meshery provider related info.
	ProviderCookieName = "meshery-provider"

	// Stores the JWT issued by the remote provider to provide secure access to its API
	TokenCookieName = "token"

	// Stores the remote provider session cookie (identity cookie) to facilitate logout from remote provider as user logs out of Meshery
	ProviderSessionCookieName = "session_cookie"
)

// JWK - a type respresting the JSON web Key
type JWK map[string]string

// SafeClose is a helper function help to close the io
func SafeClose(co io.Closer, log logger.Handler) {
	if cerr := co.Close(); cerr != nil {
		log.Error(ErrCloseIoReader(cerr))
	}
}

// DoRequest - executes a request and does refreshing automatically
func (l *RemoteProvider) DoRequest(req *http.Request, tokenString string) (*http.Response, error) {
	resp, err := l.doRequestHelper(req, tokenString)
	if err != nil {
		return nil, ErrDoRequest(err, req.Method, req.URL.String())
	}

	if resp.StatusCode == 401 {
		// Read and close response body before reusing request
		// https://github.com/golang/go/issues/19653#issuecomment-341540384
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		l.Log.Warn(ErrTokenRetry)
		newToken, err := l.refreshToken(tokenString)
		if err != nil {
			return nil, ErrTokenRefresh(err)
		}
		l.Log.Info("token refresh successful")
		resp, err := l.doRequestHelper(req, newToken)
		if err != nil {
			return nil, ErrDoRequest(err, req.Method, req.URL.String())
		}
		return resp, nil
	}
	return resp, err
}

// refreshToken - takes a tokenString and returns a new tokenString
func (l *RemoteProvider) refreshToken(tokenString string) (string, error) {
	l.TokenStoreMut.Lock()
	defer l.TokenStoreMut.Unlock()
	newTokenString := l.TokenStore[tokenString]
	if newTokenString != "" {
		return newTokenString, nil
	}
	bd := map[string]string{
		TokenCookieName: tokenString,
	}
	jsonString, err := json.Marshal(bd)
	if err != nil {
		return "", ErrMarshal(err, "refreshing token")
	}
	r, err := http.Post(l.RemoteProviderURL+"/refresh", "application/json; charset=utf-8", bytes.NewReader(jsonString))
	if err != nil {
		return "", err
	}
	if r.StatusCode == http.StatusInternalServerError {
		return "", ErrTokenRefresh(fmt.Errorf("failed to refresh token. Status code 500."))
	}

	defer SafeClose(r.Body, l.Log)
	var target map[string]string
	err = json.NewDecoder(r.Body).Decode(&target)
	if err != nil {
		return "", err
	}
	l.TokenStore[tokenString] = target[TokenCookieName]
	time.AfterFunc(1*time.Hour, func() {
		l.Log.Info("deleting old token string")
		delete(l.TokenStore, tokenString)
	})
	return target[TokenCookieName], nil
}

func (l *RemoteProvider) doRequestHelper(req *http.Request, token string) (*http.Response, error) {
	c := &http.Client{}
	req.Header.Set("Authorization", fmt.Sprintf("bearer %s", token))
	// if token == models.GlobalTokenForAnonymousResults { // disabling because of import cycle
	req.Header.Set("X-API-Key", token) // adds the token as special passphrase incase the token is a special passphrase
	// }
	req.Header.Set("SystemID", viper.GetString("INSTANCE_ID")) // Adds the system id to the header for event tracking
	resp, err := c.Do(req)
	if err != nil {
		return nil, ErrTokenClientCheck(err)
	}
	return resp, nil
}

// GetToken - extracts token form a request
func (l *RemoteProvider) GetToken(req *http.Request) (string, error) {
	ck, err := req.Cookie(TokenCookieName)
	if err != nil {
		return "", ErrGetToken(err)
	}
	return ck.Value, nil
}

// DecodeTokenData - Decodes a tokenString to a token
func (l *RemoteProvider) DecodeTokenData(tokenStringB64 string) (*oauth2.Token, error) {
	var token oauth2.Token
	// logrus.Debugf("Token string %s", tokenStringB64)
	tokenString, err := base64.RawStdEncoding.DecodeString(tokenStringB64)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(tokenString, &token)
	if err != nil {
		return nil, ErrUnmarshal(err, "Token String")
	}
	return &token, nil
}

// UpdateJWKs - Updates Keys to the JWKS
func (l *RemoteProvider) UpdateJWKs() error {
	resp, err := http.Get(l.RemoteProviderURL + "/keys")
	if err != nil {
		return ErrJWKsKeys(err)
	}
	defer SafeClose(resp.Body, l.Log)
	jsonDataFromHTTP, err := io.ReadAll(resp.Body)
	if err != nil {
		return ErrDataRead(err, "Response Body")
	}
	jwksJSON := map[string][]map[string]string{}
	if err := json.Unmarshal([]byte(jsonDataFromHTTP), &jwksJSON); err != nil {
		return ErrUnmarshal(err, "JWKs Keys")
	}

	jwks := jwksJSON["keys"]

	if jwks == nil {
		return ErrNilJWKs
	}

	l.Keys = jwks

	return nil
}

// GetJWK - Takes a keyId and returns the JWK
func (l *RemoteProvider) GetJWK(kid string) (JWK, error) {
	for _, key := range l.Keys {
		if key["kid"] == kid {
			return key, nil
		}
	}
	err := l.UpdateJWKs()
	if err != nil {
		return nil, err
	}
	for _, key := range l.Keys {
		if key["kid"] == kid {
			return key, nil
		}
	}
	return nil, ErrNilKeys
}

// GenerateKey - generate the actual key from the JWK
func (l *RemoteProvider) GenerateKey(jwk JWK) (*rsa.PublicKey, error) {
	// decode the base64 bytes for n
	nb, err := base64.RawURLEncoding.DecodeString(jwk["n"])
	if err != nil {
		return nil, ErrDecodeBase64(err, "JWK")
	}

	e := 0
	// The default exponent is usually 65537, so just compare the
	// base64 for [1,0,1] or [0,1,0,1]
	if jwk["e"] == "AQAB" || jwk["e"] == "AAEAAQ" {
		e = 65537
	} else {
		// need to decode "e" as a big-endian int
		return nil, ErrDecodeBase64(err, "JWK as big-endian int")
	}

	pk := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: e,
	}

	der, err := x509.MarshalPKIXPublicKey(pk)
	if err != nil {
		return nil, ErrMarshalPKIX(err)
	}

	block := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: der,
	}

	var out bytes.Buffer
	if err := pem.Encode(&out, block); err != nil {
		return nil, ErrEncodingPEM(err)
	}
	return jwt.ParseRSAPublicKeyFromPEM(out.Bytes())
}

// VerifyToken - verifies the validity of a tokenstring
func (l *RemoteProvider) VerifyToken(tokenString string) (*jwt.MapClaims, error) {
	dtoken, err := l.DecodeTokenData(tokenString)
	if err != nil {
		return nil, ErrTokenDecode(err)
	}
	tokenString = dtoken.AccessToken
	tokenUP, x, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, ErrPraseUnverified(err)
	}
	kid := tokenUP.Header["kid"].(string)

	var jtk map[string]interface{}
	t, _ := base64.RawStdEncoding.DecodeString(x[1])
	if err := json.Unmarshal(t, &jtk); err != nil {
		return nil, ErrPraseUnverified(err)
	}

	// TODO: Once hydra fixes https://github.com/ory/hydra/issues/1542
	// we should rather configure hydra auth server to remove nbf field in the token
	_, ok := jtk["exp"]
	if ok {
		exp := int64(jtk["exp"].(float64))
		if time.Now().Unix()  > exp {
			return nil, ErrTokenExpired
		}
	}

	keyJSON, err := l.GetJWK(kid)
	if err != nil {
		return nil, err
	}
	key, err := l.GenerateKey(keyJSON)
	if err != nil {
		return nil, err
	}

	// Verifies the signature
tokenParser := jwt.NewParser()
	token, err := tokenParser.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return key, nil
	})

	if err != nil {
		return nil, ErrTokenPrase(err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenClaims
	}
	return &claims, nil
}

func (l *RemoteProvider) revokeToken(tokenString string) error {
	jsonData := make(map[string]string)
	token, err := l.DecodeTokenData(tokenString)

	if err != nil {
		return ErrTokenDecode(err)
	}

	jsonData["token"] = token.RefreshToken

	body, err := json.Marshal(jsonData)

	if err != nil {
		return ErrMarshal(err, "refresh token")
	}

	remoteURL, err := url.Parse(fmt.Sprintf("%s/revoke", l.RemoteProviderURL))
	if err != nil {
		l.Log.Error(ErrUrlParse(err))
		return err
	}
	r, err := http.Post(remoteURL.String(), "application/json", bytes.NewReader(body))

	if err != nil {
		err = ErrTokenRevoke(err)
		l.Log.Error(err)
		return err
	}

	if r.StatusCode != http.StatusOK {
		return ErrTokenRevoke(fmt.Errorf("failed to revoke token: status %d", r.StatusCode))
	}
	return nil
}

func (l *RemoteProvider) introspectToken(tokenString string) error {
	jsonData := make(map[string]string)
	token, err := l.DecodeTokenData(tokenString)

	if err != nil {
		return ErrTokenDecode(err)
	}

	jsonData["token"] = token.AccessToken
	body, err := json.Marshal(jsonData)

	if err != nil {
		return ErrMarshal(err, "refresh token")
	}

	remoteURL, err := url.Parse(fmt.Sprintf("%s/introspect", l.RemoteProviderURL))
	if err != nil {
		l.Log.Error(ErrUrlParse(err))
		return err
	}
	r, err := http.Post(remoteURL.String(), "application/json", bytes.NewReader(body))
	if err != nil {
		err = ErrTokenIntrospect(err)
		l.Log.Error(err)
		return err
	}

	if r.StatusCode == http.StatusUnauthorized {
		return ErrTokenIntrospect(fmt.Errorf("unauthorized access: status %d", r.StatusCode))
	}

	if r.StatusCode != http.StatusOK {
		return ErrTokenIntrospect(fmt.Errorf("failed to introspect token: status %d", r.StatusCode))
	}

	return nil
}

func setCookie(w http.ResponseWriter, name, value string, duration time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(duration),
	})
}

func unsetCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (l *RemoteProvider) SetProviderCookie(w http.ResponseWriter, provider string) {
	setCookie(w, ProviderCookieName, provider, l.CookieDuration)
}

func (l *RemoteProvider) UnSetProviderCookie(w http.ResponseWriter) {
	unsetCookie(w, ProviderCookieName)
}

func (l *RemoteProvider) SetJWTCookie(w http.ResponseWriter, token string) {
	setCookie(w, TokenCookieName, token, l.CookieDuration)
}

func (l *RemoteProvider) UnSetJWTCookie(w http.ResponseWriter) {
	unsetCookie(w, TokenCookieName)
}

func (l *RemoteProvider) SetProviderSessionCookie(w http.ResponseWriter, sessionCookie string) {
	setCookie(w, ProviderSessionCookieName, sessionCookie, l.CookieDuration)
}

func (l *RemoteProvider) UnSetProviderSessionCookie(w http.ResponseWriter) {
	unsetCookie(w, ProviderSessionCookieName)
}
