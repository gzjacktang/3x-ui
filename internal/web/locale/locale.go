package locale

import (
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var (
	i18nBundle   *i18n.Bundle
	LocalizerWeb *i18n.Localizer
)

// I18nType represents the type of interface for internationalization.
type I18nType string

const (
	Web I18nType = "web" // Web interface type
)

// InitLocalizer initializes the internationalization system with embedded translation files.
func InitLocalizer(i18nFS embed.FS) error {
	// set default bundle to English
	i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
	i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// parse files
	if err := parseTranslationFiles(i18nFS, i18nBundle); err != nil {
		return err
	}

	return nil
}

// createTemplateData creates a template data map from parameters with optional separator.
func createTemplateData(params []string, separator ...string) map[string]any {
	sep := "=="
	if len(separator) > 0 {
		sep = separator[0]
	}

	templateData := make(map[string]any)
	for _, param := range params {
		parts := strings.SplitN(param, sep, 2)
		templateData[parts[0]] = parts[1]
	}

	return templateData
}

// I18n retrieves a localized message for the given key and type.
// It supports both bot and web contexts, with optional template parameters.
// Returns the localized message or an empty string if localization fails.
func I18n(i18nType I18nType, key string, params ...string) string {
	if i18nType != Web {
		logger.Errorf("Invalid type for I18n: %s", i18nType)
		return ""
	}

	templateData := createTemplateData(params)

	if LocalizerWeb == nil {
		return key
	}

	msg, err := LocalizerWeb.Localize(&i18n.LocalizeConfig{
		MessageID:    key,
		TemplateData: templateData,
	})
	if err != nil {
		logger.Errorf("Failed to localize message: %v", err)
		return ""
	}

	return msg
}

// LocalizerMiddleware returns a Gin middleware that sets up localization for web requests.
// It determines the user's language from cookies or Accept-Language header,
// creates a localizer instance, and stores it in the Gin context for use in handlers.
// Also provides the I18n function in the context for template rendering.
func LocalizerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Ensure bundle is initialized so creating a Localizer won't panic
		if i18nBundle == nil {
			i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
			i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)
			if err := loadTranslationsFromDisk(i18nBundle); err != nil {
				logger.Warning("i18n lazy load failed:", err)
			}
		}
		var lang string

		if cookie, err := c.Request.Cookie("lang"); err == nil {
			lang = cookie.Value
		} else {
			lang = c.GetHeader("Accept-Language")
		}

		LocalizerWeb = i18n.NewLocalizer(i18nBundle, lang)

		c.Set("localizer", LocalizerWeb)
		c.Set("I18n", I18n)
		c.Next()
	}
}

// loadTranslationsFromDisk attempts to load translation files from "internal/web/translation" using the local filesystem.
func loadTranslationsFromDisk(bundle *i18n.Bundle) error {
	root := os.DirFS("internal/web")
	return fs.WalkDir(root, "translation", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		_, err = bundle.ParseMessageFileBytes(data, path)
		return err
	})
}

// parseTranslationFiles parses embedded translation files and adds them to the i18n bundle.
func parseTranslationFiles(i18nFS embed.FS, i18nBundle *i18n.Bundle) error {
	err := fs.WalkDir(i18nFS, "translation",
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			data, err := i18nFS.ReadFile(path)
			if err != nil {
				return err
			}

			_, err = i18nBundle.ParseMessageFileBytes(data, path)
			return err
		})
	if err != nil {
		return err
	}

	return nil
}
