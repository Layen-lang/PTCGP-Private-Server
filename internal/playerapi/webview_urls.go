package playerapi

import (
	"context"
	"net/url"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
)

const (
	officialSiteURL  = "https://www.apppokemon.com/tcgp/"
	privacyURL       = "https://www.apppokemon.com/tcgp/kiyaku/kiyaku001/policy/detail/fr/"
	termsURL         = "https://www.apppokemon.com/tcgp/kiyaku/kiyaku001/rule/detail/fr/"
	noticeCDNBaseURL = "https://cdn-prod-assets-notice-app-41283.cdn-dena.com"
)

func (startupWebviewServer) GetAboutUrlV1(context.Context, *api.WebviewGetAboutUrlV1_Types_Request) (*api.WebviewGetAboutUrlV1_Types_Response, error) {
	return &api.WebviewGetAboutUrlV1_Types_Response{Url: officialSiteURL}, nil
}

func (startupWebviewServer) GetECommerceLawUrlV1(context.Context, *api.WebviewGetECommerceLawUrlV1_Types_Request) (*api.WebviewGetECommerceLawUrlV1_Types_Response, error) {
	return &api.WebviewGetECommerceLawUrlV1_Types_Response{Url: officialSiteURL}, nil
}

func (startupWebviewServer) GetGameHintUrlV1(_ context.Context, request *api.WebviewGetGameHintUrlV1_Types_Request) (*api.WebviewGetGameHintUrlV1_Types_Response, error) {
	mode := "A"
	if request.GetIsBMode() {
		mode = "B"
	}
	values := url.Values{"lang": {webviewLocale(request.GetLanguage())}, "mode": {mode}, "os": {"Android"}}
	return &api.WebviewGetGameHintUrlV1_Types_Response{Url: noticeCDNBaseURL + "/webview/gamehint/?" + values.Encode()}, nil
}

func webviewLocale(language api.WebviewLanguage) string {
	switch language {
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_DE:
		return "de_DE"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_EN_US:
		return "en_US"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_EN_GB:
		return "en_GB"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_ES:
		return "es_ES"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_IT:
		return "it_IT"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_PT_BR:
		return "pt_BR"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_JA:
		return "ja_JP"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_ZH_TW:
		return "zh_TW"
	case api.WebviewLanguage_WEBVIEW_LANGUAGE_KO:
		return "ko_KR"
	default:
		return "fr_FR"
	}
}

func (startupWebviewServer) GetHowToUpdateUrlV1(context.Context, *api.WebviewGetHowToUpdateUrlV1_Types_Request) (*api.WebviewGetHowToUpdateUrlV1_Types_Response, error) {
	return &api.WebviewGetHowToUpdateUrlV1_Types_Response{Url: officialSiteURL}, nil
}

func (startupWebviewServer) GetNewsDetailUrlV1(context.Context, *api.WebviewGetNewsDetailUrlV1_Types_Request) (*api.WebviewGetNewsDetailUrlV1_Types_Response, error) {
	return &api.WebviewGetNewsDetailUrlV1_Types_Response{Url: officialSiteURL}, nil
}

func (startupWebviewServer) GetPaymentServiceActUrlV1(context.Context, *api.WebviewGetPaymentServiceActUrlV1_Types_Request) (*api.WebviewGetPaymentServiceActUrlV1_Types_Response, error) {
	return &api.WebviewGetPaymentServiceActUrlV1_Types_Response{Url: officialSiteURL}, nil
}

func (startupWebviewServer) GetSpecifiedCommercialTransactionLawUrlV1(context.Context, *api.WebviewGetSpecifiedCommercialTransactionLawUrlV1_Types_Request) (*api.WebviewGetSpecifiedCommercialTransactionLawUrlV1_Types_Response, error) {
	return &api.WebviewGetSpecifiedCommercialTransactionLawUrlV1_Types_Response{Url: officialSiteURL}, nil
}
