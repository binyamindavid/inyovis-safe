import type { ProblemCode } from './generated/types.gen';

export type Locale = 'en' | 'fr' | 'he';

const messages: Record<ProblemCode, Record<Locale, string>> = {
  UNAUTHORIZED: { en: 'Please sign in.', fr: 'Veuillez vous connecter.', he: 'יש להתחבר.' },
  FORBIDDEN: { en: 'This request was blocked.', fr: 'Cette demande a été bloquée.', he: 'הבקשה נחסמה.' },
  VALIDATION_ERROR: { en: 'Check the entered information.', fr: 'Vérifiez les informations saisies.', he: 'יש לבדוק את הפרטים שהוזנו.' },
  INVALID_CODE: { en: 'That code is not valid. Try again.', fr: 'Ce code est invalide. Réessayez.', he: 'הקוד אינו תקין. נסו שוב.' },
  EXPIRED_CODE: { en: 'That code has expired. Request a new one.', fr: 'Ce code a expiré. Demandez-en un autre.', he: 'תוקף הקוד פג. בקשו קוד חדש.' },
  RATE_LIMITED: { en: 'Too many attempts. Try again later.', fr: 'Trop de tentatives. Réessayez plus tard.', he: 'יותר מדי ניסיונות. נסו שוב מאוחר יותר.' },
  UPSTREAM_UNAVAILABLE: { en: 'The service is unavailable.', fr: 'Le service est indisponible.', he: 'השירות אינו זמין.' },
  UPSTREAM_TIMEOUT: { en: 'The service took too long to respond.', fr: 'Le service a mis trop de temps à répondre.', he: 'זמן ההמתנה לתשובת השירות הסתיים.' },
  INTERNAL_ERROR: { en: 'Something went wrong.', fr: 'Une erreur est survenue.', he: 'אירעה שגיאה.' },
  NOT_FOUND: { en: 'This archive or invitation is unavailable.', fr: 'Cette archive ou invitation est indisponible.', he: 'הארכיון או ההזמנה אינם זמינים.' },
  CONFLICT: { en: 'This change cannot be made now.', fr: 'Cette modification est impossible pour le moment.', he: 'לא ניתן לבצע את השינוי כעת.' },
  UPLOAD_STAGED_OBJECT_MISSING: { en: 'The file did not reach storage. Retry the upload.', fr: 'Le fichier n’est pas arrivé au stockage. Réessayez le dépôt.', he: 'הקובץ לא הגיע לאחסון. יש לנסות להעלות שוב.' },
};

export function problemMessage(code: ProblemCode, locale: Locale): string {
  return messages[code]?.[locale] ?? messages.INTERNAL_ERROR[locale];
}
