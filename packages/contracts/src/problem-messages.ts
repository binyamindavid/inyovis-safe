import type { ProblemCode } from './generated/types.gen';

export type Locale = 'en' | 'fr' | 'he';

const messages: Record<ProblemCode, Record<Locale, string>> = {
  UNAUTHORIZED: { en: 'Please sign in.', fr: 'Veuillez vous connecter.', he: 'יש להתחבר.' },
  VALIDATION_ERROR: { en: 'Check the entered information.', fr: 'Vérifiez les informations saisies.', he: 'יש לבדוק את הפרטים שהוזנו.' },
  UPSTREAM_UNAVAILABLE: { en: 'The service is unavailable.', fr: 'Le service est indisponible.', he: 'השירות אינו זמין.' },
  UPSTREAM_TIMEOUT: { en: 'The service took too long to respond.', fr: 'Le service a mis trop de temps à répondre.', he: 'זמן ההמתנה לתשובת השירות הסתיים.' },
  INTERNAL_ERROR: { en: 'Something went wrong.', fr: 'Une erreur est survenue.', he: 'אירעה שגיאה.' },
};

export function problemMessage(code: ProblemCode, locale: Locale): string {
  return messages[code]?.[locale] ?? messages.INTERNAL_ERROR[locale];
}
