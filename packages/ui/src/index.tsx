'use client';
import { Button as AriaButton, Checkbox as AriaCheckbox, DateField as AriaDateField, DateInput, DateSegment, Dialog, DialogTrigger, FieldError, Heading, Input, I18nProvider, Label, ListBox, ListBoxItem, Modal, ModalOverlay, Popover, Select as AriaSelect, SelectValue, Text, TextField as AriaTextField, useLocale } from 'react-aria-components';
import { parseDate, type CalendarDate } from '@internationalized/date';
import type * as React from 'react';
import type { ReactNode } from 'react';

export { I18nProvider, useLocale };
export function Button({ children, variant = 'primary', ...props }: React.ComponentProps<typeof AriaButton> & { variant?: 'primary' | 'secondary' | 'quiet' | 'danger' }) {
  return <AriaButton {...props} className={`ui-button ui-button-${variant} ${props.className ?? ''}`}>{children}</AriaButton>;
}
export function Checkbox({children,...props}:Omit<React.ComponentProps<typeof AriaCheckbox>,'children'> & {children:ReactNode}) { return <AriaCheckbox {...props} className={`ui-checkbox ${props.className ?? ''}`}><span className="ui-checkbox-box" aria-hidden="true" />{children}</AriaCheckbox>; }
export function TextField({ label, description, error, inputProps, ...props }: Omit<React.ComponentProps<typeof AriaTextField>, 'children'> & { label: string; description?: string; error?: string; inputProps?: React.ComponentProps<typeof Input> }) {
  return <AriaTextField {...props} isInvalid={Boolean(error) || props.isInvalid} className={`ui-field ${props.className ?? ''}`}>
    <Label>{label}</Label><Input {...inputProps} className={`ui-input ${inputProps?.className ?? ''}`} />
    {description && <Text slot="description" className="ui-help">{description}</Text>}
    {error && <FieldError>{error}</FieldError>}
  </AriaTextField>;
}
export type SelectOption = { id: string; label: string };
export function Select({ label, options, ...props }: Omit<React.ComponentProps<typeof AriaSelect>, 'children'> & { label: string; options: SelectOption[] }) {
  return <AriaSelect {...props} className={`ui-field ${props.className ?? ''}`}>
    <Label>{label}</Label><AriaButton className="ui-select-trigger"><SelectValue /><span aria-hidden="true" className="ui-chevron">⌄</span></AriaButton>
    <Popover className="ui-popover"><ListBox className="ui-listbox" items={options}>{item => <ListBoxItem id={item.id} textValue={item.label} className="ui-option"><bdi dir="auto">{item.label}</bdi></ListBoxItem>}</ListBox></Popover>
  </AriaSelect>;
}
export function ConfirmDialog({ trigger, title, children, closeLabel, action }: { trigger: ReactNode; title: string; children: ReactNode; closeLabel: string; action?: ReactNode }) {
  return <DialogTrigger><AriaButton className="ui-button ui-button-secondary">{trigger}</AriaButton><ModalOverlay className="ui-overlay" isDismissable><Modal className="ui-modal"><Dialog className="ui-dialog"><Heading slot="title">{title}</Heading><div>{children}</div><div className="ui-dialog-actions"><AriaButton slot="close" className="ui-button ui-button-secondary">{closeLabel}</AriaButton>{action}</div></Dialog></Modal></ModalOverlay></DialogTrigger>;
}
export function DateField({ label, value, onChange, description }: { label: string; value: string; onChange?: (value: string) => void; description?: string }) {
  return <AriaDateField value={parseDate(value)} onChange={(date: CalendarDate | null) => { if (date) onChange?.(date.toString()); }} className="ui-field">
    <Label>{label}</Label><DateInput className="ui-date-input">{segment => <DateSegment segment={segment} className="ui-date-segment" />}</DateInput>
    {description && <Text slot="description" className="ui-help">{description}</Text>}
  </AriaDateField>;
}
