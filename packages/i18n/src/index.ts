export { resolveLanguage } from "./resolveLanguage";
export {
  setupI18n,
  changeLanguage,
  SUPPORTED_LANGUAGES,
  FALLBACK_LANGUAGE,
  LANGUAGE_AUTONYMS,
  type SupportedLanguage,
} from "./setup";
export { useTranslation, Trans } from "react-i18next";
export { translateApiError, type ServerError } from "./translateApiError";
