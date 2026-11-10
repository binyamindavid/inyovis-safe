export type Locale = 'en' | 'fr' | 'he';
export const locale = (): Locale => {
  if (typeof navigator === 'undefined') return 'en';
  const value = navigator.language.toLowerCase();
  return value.startsWith('he') ? 'he' : value.startsWith('fr') ? 'fr' : 'en';
};
export const intent = { 'X-Invoyis-Intent': 'browser' as const };
export const words = {
  en: { archives: 'Archives', create: 'Create archive', personal: 'Personal', business: 'Business', name: 'Name', jurisdiction: 'Jurisdiction (two-letter country code)', members: 'Members', invite: 'Invite member', email: 'Email address', role: 'Role', export: 'Allow bulk export', save: 'Save', remove: 'Remove', revoke: 'Revoke', accept: 'Accept invitation', signIn: 'Sign in to accept', loading: 'Loading…', language: 'Language', back: 'Back to archives', empty: 'No archives yet', error: 'Something went wrong. Please try again.', admin: 'Admin', contributor: 'Contributor', viewer: 'Viewer', invited: 'Invitation sent', switch: 'Open archive' },
  fr: { archives: 'Archives', create: 'Créer une archive', personal: 'Personnelle', business: 'Entreprise', name: 'Nom', jurisdiction: 'Juridiction (code pays à deux lettres)', members: 'Membres', invite: 'Inviter un membre', email: 'Adresse e-mail', role: 'Rôle', export: 'Autoriser l’export groupé', save: 'Enregistrer', remove: 'Retirer', revoke: 'Révoquer', accept: 'Accepter l’invitation', signIn: 'Se connecter pour accepter', loading: 'Chargement…', language: 'Langue', back: 'Retour aux archives', empty: 'Aucune archive', error: 'Une erreur est survenue. Réessayez.', admin: 'Admin', contributor: 'Contributeur', viewer: 'Lecteur', invited: 'Invitation envoyée', switch: 'Ouvrir l’archive' },
  he: { archives: 'ארכיונים', create: 'יצירת ארכיון', personal: 'אישי', business: 'עסקי', name: 'שם', jurisdiction: 'תחום שיפוט (קוד מדינה בן שתי אותיות)', members: 'חברים', invite: 'הזמנת חבר', email: 'כתובת אימייל', role: 'תפקיד', export: 'אישור ייצוא מרוכז', save: 'שמירה', remove: 'הסרה', revoke: 'ביטול', accept: 'קבלת ההזמנה', signIn: 'כניסה לקבלת ההזמנה', loading: 'טוען…', language: 'שפה', back: 'חזרה לארכיונים', empty: 'עדיין אין ארכיונים', error: 'אירעה שגיאה. נסו שוב.', admin: 'מנהל', contributor: 'תורם', viewer: 'צופה', invited: 'ההזמנה נשלחה', switch: 'פתיחת ארכיון' },
} satisfies Record<Locale, Record<string, string>>;
