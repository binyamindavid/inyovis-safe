export type FolderItem = { id: string; displayName: string; archived: boolean; count: number };
export const FOLDER_NONE: 'none';
export function orderFolders(items: FolderItem[], unassigned: number, locale: string, unassignedName: string): FolderItem[];
export function folderCountLabel(words: { folderCountNone: string; folderCountOne: string; folderCountMany: string }, count: number, locale: string): string;
export function monogram(name: string): string;
export function folderTone(id: string): string;
