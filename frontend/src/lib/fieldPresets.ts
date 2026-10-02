import { t as appT, type MessageKey } from '../i18n'
import type { CustomField, CustomFieldInput, CustomFieldType } from './api/customFields'

type Translate = (key: MessageKey) => string

/** A ready-made set of fields for one kind of paperwork, in the UI language. */
export type FieldPreset = { title: string; fields: CustomFieldInput[] }

/** One definition per field, shared by every preset that lists it, so presets never clash. */
export function fieldPresets(t: Translate = appT): FieldPreset[] {
  const field = (
    type: CustomFieldType,
    name: MessageKey,
    hint: MessageKey,
    ...choices: MessageKey[]
  ): CustomFieldInput => ({
    type,
    name: t(name),
    description: t(hint),
    choices: choices.map((c) => ({ id: '', name: t(c) })),
  })

  const invoiceNumber = field('text', 'fieldPresets.invoiceNumber', 'fieldPresets.invoiceNumberHint')
  const amount = field('number', 'fieldPresets.amount', 'fieldPresets.amountHint')
  const currency = field('text', 'fieldPresets.currency', 'fieldPresets.currencyHint')
  const customerNumber = field('text', 'fieldPresets.customerNumber', 'fieldPresets.customerNumberHint')
  const validUntil = field('date', 'fieldPresets.validUntil', 'fieldPresets.validUntilHint')
  const startDate = field('date', 'fieldPresets.startDate', 'fieldPresets.startDateHint')
  const noticePeriod = field('text', 'fieldPresets.noticePeriod', 'fieldPresets.noticePeriodHint')
  const fileReference = field('text', 'fieldPresets.fileReference', 'fieldPresets.fileReferenceHint')
  const responseDeadline = field(
    'date',
    'fieldPresets.responseDeadline',
    'fieldPresets.responseDeadlineHint',
  )

  return [
    {
      title: t('fieldPresets.invoices'),
      fields: [
        invoiceNumber,
        amount,
        currency,
        field('date', 'fieldPresets.dueDate', 'fieldPresets.dueDateHint'),
        field(
          'choice',
          'fieldPresets.paymentStatus',
          'fieldPresets.paymentStatusHint',
          'fieldPresets.paymentOpen',
          'fieldPresets.paymentPaid',
        ),
      ],
    },
    {
      title: t('fieldPresets.bookkeeping'),
      fields: [
        field(
          'choice',
          'fieldPresets.direction',
          'fieldPresets.directionHint',
          'fieldPresets.directionIncoming',
          'fieldPresets.directionOutgoing',
        ),
        invoiceNumber,
        field('number', 'fieldPresets.netAmount', 'fieldPresets.netAmountHint'),
        field('number', 'fieldPresets.vatRate', 'fieldPresets.vatRateHint'),
        field('number', 'fieldPresets.vatAmount', 'fieldPresets.vatAmountHint'),
        amount,
        currency,
        field('date', 'fieldPresets.discountUntil', 'fieldPresets.discountUntilHint'),
        customerNumber,
      ],
    },
    {
      title: t('fieldPresets.orders'),
      fields: [
        field('text', 'fieldPresets.quoteNumber', 'fieldPresets.quoteNumberHint'),
        field('text', 'fieldPresets.orderNumber', 'fieldPresets.orderNumberHint'),
        field('text', 'fieldPresets.deliveryNoteNumber', 'fieldPresets.deliveryNoteNumberHint'),
        validUntil,
        amount,
        currency,
      ],
    },
    {
      title: t('fieldPresets.contracts'),
      fields: [
        field('text', 'fieldPresets.contractNumber', 'fieldPresets.contractNumberHint'),
        customerNumber,
        startDate,
        field('date', 'fieldPresets.endDate', 'fieldPresets.endDateHint'),
        noticePeriod,
        field('number', 'fieldPresets.monthlyCost', 'fieldPresets.monthlyCostHint'),
        currency,
      ],
    },
    {
      title: t('fieldPresets.leases'),
      fields: [
        field('text', 'fieldPresets.propertyAddress', 'fieldPresets.propertyAddressHint'),
        startDate,
        field('number', 'fieldPresets.baseRent', 'fieldPresets.baseRentHint'),
        field('number', 'fieldPresets.serviceCharges', 'fieldPresets.serviceChargesHint'),
        field('number', 'fieldPresets.deposit', 'fieldPresets.depositHint'),
        currency,
        noticePeriod,
      ],
    },
    {
      title: t('fieldPresets.insurance'),
      fields: [
        field('text', 'fieldPresets.policyNumber', 'fieldPresets.policyNumberHint'),
        field('number', 'fieldPresets.premium', 'fieldPresets.premiumHint'),
        currency,
        field('date', 'fieldPresets.renewalDate', 'fieldPresets.renewalDateHint'),
      ],
    },
    {
      title: t('fieldPresets.warranty'),
      fields: [
        amount,
        currency,
        field('date', 'fieldPresets.warrantyUntil', 'fieldPresets.warrantyUntilHint'),
        field('text', 'fieldPresets.serialNumber', 'fieldPresets.serialNumberHint'),
      ],
    },
    {
      title: t('fieldPresets.taxes'),
      fields: [
        field('number', 'fieldPresets.taxYear', 'fieldPresets.taxYearHint'),
        field('text', 'fieldPresets.taxNumber', 'fieldPresets.taxNumberHint'),
        fileReference,
        responseDeadline,
        amount,
        currency,
      ],
    },
    {
      title: t('fieldPresets.legalCases'),
      fields: [
        fileReference,
        field('text', 'fieldPresets.court', 'fieldPresets.courtHint'),
        field('text', 'fieldPresets.opposingParty', 'fieldPresets.opposingPartyHint'),
        field('date', 'fieldPresets.hearingDate', 'fieldPresets.hearingDateHint'),
        responseDeadline,
        field('number', 'fieldPresets.amountInDispute', 'fieldPresets.amountInDisputeHint'),
        currency,
      ],
    },
    {
      title: t('fieldPresets.deeds'),
      fields: [
        field('text', 'fieldPresets.deedNumber', 'fieldPresets.deedNumberHint'),
        field('text', 'fieldPresets.grantor', 'fieldPresets.grantorHint'),
        field('text', 'fieldPresets.agent', 'fieldPresets.agentHint'),
        validUntil,
      ],
    },
    {
      title: t('fieldPresets.payslips'),
      fields: [
        field('text', 'fieldPresets.employee', 'fieldPresets.employeeHint'),
        field('text', 'fieldPresets.payPeriod', 'fieldPresets.payPeriodHint'),
        field('number', 'fieldPresets.grossPay', 'fieldPresets.grossPayHint'),
        field('number', 'fieldPresets.netPay', 'fieldPresets.netPayHint'),
        currency,
      ],
    },
    {
      title: t('fieldPresets.bank'),
      fields: [
        field('text', 'fieldPresets.iban', 'fieldPresets.ibanHint'),
        field('text', 'fieldPresets.statementNumber', 'fieldPresets.statementNumberHint'),
        field('number', 'fieldPresets.closingBalance', 'fieldPresets.closingBalanceHint'),
        currency,
      ],
    },
  ]
}

const nameKey = (name: string) => name.trim().toLowerCase()

/** The preset's fields with no defined field of the same name, ignoring case. */
export function missingFields(preset: FieldPreset, defined: CustomField[]): CustomFieldInput[] {
  const names = new Set(defined.map((f) => nameKey(f.name)))
  return preset.fields.filter((f) => !names.has(nameKey(f.name)))
}
