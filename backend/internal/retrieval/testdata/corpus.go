// Package testdata holds the synthetic archive the retrieval evaluations run
// against, and the queries they are scored on.
//
// It is a package rather than a fixture file so both the Bleve evaluation
// (internal/fulltext) and the fusion evaluation (internal/retrieval) score the
// same corpus with the same expectations, and a change to one is visible to the
// other. It lives under testdata/ so it is never linked into the binary.
//
// The documents imitate what the archive actually holds: a German, English or
// Ukrainian body under English metadata, because the pipeline writes a
// normalised English title and summary beside the original ones. Cross-language
// recall in this product is largely a metadata effect, and a corpus with
// monolingual documents would not show that.
//
// Nothing here is real. Names, numbers and addresses are invented.
package testdata

import "time"

// Document is one synthetic archive entry, in the shape the index is fed.
type Document struct {
	ID     string
	User   string
	Locale string

	// Title and Summary are the normalised English metadata; the *Original
	// fields are what the document itself says.
	Title         string
	TitleOriginal string
	Purpose       string
	Summary       string
	DocumentType  string
	Correspondent string
	Tags          []string
	Date          time.Time
	Text          string
}

// Filters is the structured part of a query.
type Filters struct {
	DateFrom      string
	DateTo        string
	Tags          []string
	DocumentType  string
	Correspondent string
}

// Kind labels what a case is testing, so an evaluation can report which class
// of query it is losing rather than only that the average dropped.
type Kind string

const (
	KindExact         Kind = "exact"
	KindCrossLanguage Kind = "cross-language"
	KindTypo          Kind = "typo"
	KindMorphology    Kind = "morphology"
	KindParaphrase    Kind = "paraphrase"
	KindFilter        Kind = "filter"
	KindIdentifier    Kind = "identifier"
)

// Case is one query and the documents that answer it.
type Case struct {
	Name    string
	Kind    Kind
	Query   string
	Want    []string
	Filters Filters
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Documents returns the corpus. All entries belong to user u1 except the last,
// which exists so an evaluation can prove owner scoping is still applied.
func Documents() []Document {
	return []Document{
		{
			ID:            "de-plumber",
			User:          "u1",
			Locale:        "de",
			Title:         "Plumber invoice for bathroom repair",
			TitleOriginal: "Rechnung Badsanierung",
			Purpose:       "Invoice from a plumbing company for repairing a leaking bathroom pipe.",
			Summary:       "Sanitär Meier charged 842.50 EUR for replacing a leaking pipe and retiling part of the bathroom floor. Paid by bank transfer in March 2024.",
			DocumentType:  "Invoice",
			Correspondent: "Sanitär Meier GmbH",
			Tags:          []string{"Handwerker", "Wohnung"},
			Date:          day(2024, time.March, 12),
			Text: `Sanitär Meier GmbH
Gartenstraße 14, 10827 Berlin

RECHNUNG Nr. 2024-0312

Sehr geehrte Frau Kowal,

hiermit stellen wir Ihnen die im Februar durchgeführten Arbeiten in Rechnung. Die undichte Steigleitung im Badezimmer wurde freigelegt, das schadhafte Stück ersetzt und die Wand wieder verschlossen.

Position 1: Demontage und Entsorgung der alten Leitung — 180,00 EUR
Position 2: Material, Kupferrohr und Fittings — 214,50 EUR
Position 3: Montage und Dichtheitsprüfung — 320,00 EUR
Position 4: Fliesenarbeiten am Boden — 128,00 EUR

Zwischensumme 842,50 EUR. Der Betrag enthält die gesetzliche Mehrwertsteuer.

Zahlbar innerhalb von 14 Tagen ohne Abzug auf das unten genannte Konto. Für Rückfragen zur Rechnung steht Ihnen unsere Buchhaltung zur Verfügung.`,
		},
		{
			ID:            "de-lease",
			User:          "u1",
			Locale:        "de",
			Title:         "Apartment lease agreement Berlin",
			TitleOriginal: "Mietvertrag über Wohnraum",
			Purpose:       "Residential lease for a two-room apartment, with the monthly rent and the deposit.",
			Summary:       "Lease for a 62 square metre apartment in Berlin-Neukölln. Monthly base rent 1234 EUR plus 210 EUR service charges; deposit of three base rents. Started August 2022, open ended.",
			DocumentType:  "Contract",
			Correspondent: "Hausverwaltung Lindner",
			Tags:          []string{"Wohnung", "Vertrag"},
			Date:          day(2022, time.August, 1),
			Text: `MIETVERTRAG ÜBER WOHNRAUM

Zwischen der Hausverwaltung Lindner, vertreten durch Herrn Ulrich Lindner, im Folgenden Vermieter, und Frau Olena Kowal, im Folgenden Mieter, wird folgender Mietvertrag geschlossen.

§ 1 Mietsache
Vermietet wird die Wohnung im dritten Obergeschoss links, bestehend aus zwei Zimmern, Küche, Bad und Balkon, mit einer Wohnfläche von etwa 62 Quadratmetern.

§ 2 Miete
Die monatliche Kaltmiete beträgt 1234 EUR. Hinzu kommt eine Vorauszahlung auf die Betriebskosten in Höhe von 210 EUR. Die Gesamtmiete ist bis zum dritten Werktag eines jeden Monats im Voraus zu entrichten.

§ 3 Kaution
Der Mieter leistet eine Sicherheit in Höhe von drei Kaltmieten, also 3702 EUR. Die Kaution wird vom Vermieter getrennt vom übrigen Vermögen angelegt und nach Beendigung des Mietverhältnisses abgerechnet.

§ 4 Kündigung
Das Mietverhältnis läuft auf unbestimmte Zeit. Die Kündigungsfrist für den Mieter beträgt drei Monate zum Monatsende.`,
		},
		{
			ID:            "de-car-insurance",
			User:          "u1",
			Locale:        "de",
			Title:         "Car insurance policy",
			TitleOriginal: "Versicherungsschein Kraftfahrtversicherung",
			Purpose:       "Motor insurance policy with the annual premium and the deductible.",
			Summary:       "Comprehensive motor insurance for a 2018 estate car. Annual premium 512.40 EUR, deductible 300 EUR for comprehensive and 150 EUR for partial cover. Policy number AB-4711.",
			DocumentType:  "Policy",
			Correspondent: "Nordstern Versicherung AG",
			Tags:          []string{"Versicherung", "Auto"},
			Date:          day(2024, time.January, 8),
			Text: `Nordstern Versicherung AG
VERSICHERUNGSSCHEIN

Vertragsnummer AB-4711
Versicherungsnehmer: Olena Kowal

Versichertes Fahrzeug: Kombi, Erstzulassung 2018, amtliches Kennzeichen B-KO 2291.

Umfang des Versicherungsschutzes: Kraftfahrzeug-Haftpflichtversicherung sowie Vollkaskoversicherung mit Selbstbeteiligung.

Der Jahresbeitrag beträgt 512,40 EUR und ist jeweils zum 1. Februar fällig. Bei Zahlung in monatlichen Raten erhöht sich der Beitrag um einen Ratenzahlungszuschlag.

Die Selbstbeteiligung beträgt in der Vollkaskoversicherung 300 EUR je Schadenfall, in der Teilkaskoversicherung 150 EUR. Schäden sind unverzüglich, spätestens innerhalb einer Woche, dem Versicherer anzuzeigen.

Der Schadenfreiheitsrabatt entspricht der Schadenfreiheitsklasse SF 12.`,
		},
		{
			ID:            "de-tax",
			User:          "u1",
			Locale:        "de",
			Title:         "Income tax assessment 2023",
			TitleOriginal: "Einkommensteuerbescheid für 2023",
			Purpose:       "Tax office assessment for the 2023 tax year, with the refund due.",
			Summary:       "The tax office assessed the 2023 return and calculated a refund of 842 EUR, to be paid to the registered account within four weeks.",
			DocumentType:  "Notice",
			Correspondent: "Finanzamt Berlin-Neukölln",
			Tags:          []string{"Steuer"},
			Date:          day(2024, time.June, 27),
			Text: `Finanzamt Berlin-Neukölln
Steuernummer 21/455/60128

EINKOMMENSTEUERBESCHEID FÜR 2023

Die Festsetzung der Einkommensteuer für das Kalenderjahr 2023 ergibt sich aus der folgenden Berechnung.

Summe der Einkünfte aus nichtselbständiger Arbeit: 68 000 EUR. Abzüglich Werbungskosten in Höhe der geltend gemachten 2 140 EUR sowie Sonderausgaben und Vorsorgeaufwendungen.

Zu versteuerndes Einkommen: 57 320 EUR. Festgesetzte Einkommensteuer: 13 908 EUR. Bereits einbehaltene Lohnsteuer: 14 750 EUR.

Es ergibt sich eine Erstattung von 842 EUR. Der Betrag wird innerhalb von vier Wochen auf das dem Finanzamt bekannte Konto überwiesen.

Die Rechtsbehelfsbelehrung finden Sie auf der Rückseite. Gegen diesen Bescheid kann innerhalb eines Monats Einspruch eingelegt werden.`,
		},
		{
			ID:            "de-dentist",
			User:          "u1",
			Locale:        "de",
			Title:         "Dentist invoice for a crown",
			TitleOriginal: "Zahnarztrechnung Zahnersatz",
			Purpose:       "Private dental invoice for a ceramic crown, with the share the insurer covers.",
			Summary:       "Dental practice invoiced 1290.60 EUR for a ceramic crown on tooth 26. The statutory insurer covers 430 EUR; the remainder is due from the patient.",
			DocumentType:  "Invoice",
			Correspondent: "Zahnarztpraxis Dr. Hoffmann",
			Tags:          []string{"Gesundheit", "Rechnung"},
			Date:          day(2024, time.April, 18),
			Text: `Zahnarztpraxis Dr. med. dent. Hoffmann
Liquidation nach GOZ

Behandlungszeitraum: März bis April 2024
Patientin: Olena Kowal

Behandlung: Versorgung des Zahnes 26 mit einer Vollkeramikkrone. Vorbereitend wurden eine Wurzelkanalbehandlung und eine provisorische Krone durchgeführt.

Zahnersatz und Laborkosten: 964,20 EUR
Zahnärztliches Honorar: 326,40 EUR

Rechnungsbetrag insgesamt: 1290,60 EUR

Der Festzuschuss Ihrer gesetzlichen Krankenkasse beträgt 430,00 EUR und wurde bereits berücksichtigt beziehungsweise wird Ihnen nach Einreichung dieser Rechnung erstattet. Der Eigenanteil ist innerhalb von 30 Tagen zu begleichen.`,
		},
		{
			ID:            "en-hosting",
			User:          "u1",
			Locale:        "en",
			Title:         "Cloud hosting invoice",
			TitleOriginal: "Cloud hosting invoice",
			Purpose:       "Annual invoice for virtual server hosting.",
			Summary:       "Northwind Cloud invoiced 240 USD for twelve months of the small virtual server plan, including 200 GB of backup storage.",
			DocumentType:  "Invoice",
			Correspondent: "Northwind Cloud",
			Tags:          []string{"Arbeit", "Rechnung"},
			Date:          day(2024, time.February, 2),
			Text: `Northwind Cloud Ltd.
INVOICE NW-88412

Billed to: O. Kowal

Description: Small virtual server plan, twelve months, prepaid. Two virtual CPUs, 4 GB of memory, 80 GB of solid state storage, and 200 GB of backup storage in the same region.

Subtotal: 240.00 USD
VAT reverse charge applies; no tax has been added to this invoice.

Total due: 240.00 USD

Payment was collected automatically from the card ending 4417 on the invoice date. This invoice is issued for your records and no further action is required.

Your plan renews automatically. Cancel at least seven days before the renewal date to avoid being charged for the next term.`,
		},
		{
			ID:            "en-travel-insurance",
			User:          "u1",
			Locale:        "en",
			Title:         "Travel insurance certificate",
			TitleOriginal: "Travel insurance certificate",
			Purpose:       "Annual travel insurance certificate with the cover limits.",
			Summary:       "Annual multi-trip travel cover up to 50000 EUR of medical expenses, 100 EUR excess per claim, valid for trips of up to 45 days.",
			DocumentType:  "Policy",
			Correspondent: "Meridian Travel Cover",
			Tags:          []string{"Versicherung", "Reise"},
			Date:          day(2024, time.May, 3),
			Text: `MERIDIAN TRAVEL COVER
Certificate of insurance

Policy holder: Olena Kowal
Certificate number MTC-90233

This certificate confirms annual multi-trip travel insurance for the twelve months from 3 May 2024.

Medical and repatriation expenses are covered up to 50,000 EUR per insured person per trip. Cancellation cover is limited to 3,000 EUR. Baggage is covered up to 1,500 EUR, with a single item limit of 400 EUR.

An excess of 100 EUR applies to each claim, other than claims for cancellation caused by hospital admission.

Cover applies to trips of up to 45 consecutive days. Winter sports are included for a maximum of 17 days in the period of insurance. Claims must be notified within 28 days of return.`,
		},
		{
			ID:            "en-employment",
			User:          "u1",
			Locale:        "en",
			Title:         "Employment contract",
			TitleOriginal: "Employment contract",
			Purpose:       "Permanent employment contract with the salary and the notice period.",
			Summary:       "Permanent contract as a senior analyst starting September 2023. Gross annual salary 68000 EUR paid in twelve instalments, notice period three months, 28 days of leave.",
			DocumentType:  "Contract",
			Correspondent: "Halberd Analytics GmbH",
			Tags:          []string{"Arbeit", "Vertrag"},
			Date:          day(2023, time.September, 1),
			Text: `EMPLOYMENT CONTRACT

Between Halberd Analytics GmbH, Berlin, and Olena Kowal.

1. Position and duties
The employee is engaged as a senior analyst. The place of work is the Berlin office, with up to three days a week worked remotely by agreement.

2. Remuneration
The gross annual salary is 68,000 EUR, paid in twelve equal monthly instalments at the end of each calendar month. A discretionary bonus may be paid in March for the preceding financial year.

3. Working time and leave
Regular working time is 40 hours per week. The employee is entitled to 28 working days of paid annual leave per calendar year.

4. Notice
After the probationary period of six months, either party may terminate this contract with three months' notice to the end of a calendar month.`,
		},
		{
			ID:            "en-bank",
			User:          "u1",
			Locale:        "en",
			Title:         "Bank statement March 2024",
			TitleOriginal: "Bank statement March 2024",
			Purpose:       "Monthly current account statement.",
			Summary:       "Current account statement for March 2024 with a closing balance of 4512.09 EUR, including the salary credit and the rent debit.",
			DocumentType:  "Statement",
			Correspondent: "Elbe Bank",
			Tags:          []string{"Bank"},
			Date:          day(2024, time.March, 31),
			Text: `ELBE BANK
Current account statement

Account holder: Olena Kowal
Period: 1 March 2024 to 31 March 2024
Opening balance: 3,905.44 EUR

02.03 Standing order, Hausverwaltung Lindner, rent — 1,444.00 EUR
05.03 Card payment, supermarket — 96.31 EUR
12.03 Transfer, Sanitär Meier GmbH, invoice 2024-0312 — 842.50 EUR
27.03 Salary, Halberd Analytics GmbH + 3,410.22 EUR
29.03 Direct debit, mobile telephone — 24.90 EUR

Closing balance: 4,512.09 EUR

Please check this statement and report any discrepancy within six weeks. After that period the balance is deemed accepted.`,
		},
		{
			ID:            "en-vaccination",
			User:          "u1",
			Locale:        "en",
			Title:         "Vaccination record",
			TitleOriginal: "Vaccination record",
			Purpose:       "Immunisation record listing the vaccinations given and when the next is due.",
			Summary:       "Immunisation record showing a tetanus and diphtheria booster given in May 2023, with the next booster due in 2033.",
			DocumentType:  "Certificate",
			Correspondent: "Praxis am Park",
			Tags:          []string{"Gesundheit"},
			Date:          day(2023, time.May, 4),
			Text: `VACCINATION RECORD

Name: Olena Kowal
Issued by: Praxis am Park, Berlin

4 May 2023 — Tetanus and diphtheria booster, batch T4419, administered in the left upper arm. Next booster due May 2033.

11 October 2021 — Seasonal influenza vaccine, batch F2210.

3 March 2019 — Hepatitis A, second dose, completing the primary course.

This record is issued for the patient's own use. Entries are made at the time of administration and are signed by the administering practitioner.`,
		},
		{
			ID:            "uk-electricity",
			User:          "u1",
			Locale:        "uk",
			Title:         "Electricity bill February 2024",
			TitleOriginal: "Рахунок за електроенергію",
			Purpose:       "Monthly electricity bill with the meter readings and the amount due.",
			Summary:       "Electricity supplier billed 1250.40 UAH for February 2024, based on 312 kilowatt hours consumed. Payment due by the twentieth of the month.",
			DocumentType:  "Invoice",
			Correspondent: "Київенерго",
			Tags:          []string{"Комунальні"},
			Date:          day(2024, time.February, 29),
			Text: `РАХУНОК ЗА ЕЛЕКТРОЕНЕРГІЮ

Постачальник: Київенерго
Особовий рахунок: 4408123901
Період: лютий 2024 року

Показання лічильника на початок періоду: 18 452 кВт·год
Показання лічильника на кінець періоду: 18 764 кВт·год
Спожито за період: 312 кВт·год

Тариф: 4,01 грн за кіловат-годину. До сплати за спожиту електроенергію: 1 250,40 грн.

Заборгованість за попередні періоди відсутня. Просимо сплатити рахунок до двадцятого числа поточного місяця, інакше буде нарахована пеня.

Показання лічильника можна передати через особистий кабінет або за телефоном контакт-центру.`,
		},
		{
			ID:            "uk-lease",
			User:          "u1",
			Locale:        "uk",
			Title:         "Apartment rental agreement Kyiv",
			TitleOriginal: "Договір оренди квартири",
			Purpose:       "Rental agreement for an apartment in Kyiv with the monthly rent.",
			Summary:       "Rental agreement for a one-room apartment in Kyiv at 15000 UAH per month plus utilities, for a term of one year from March 2023.",
			DocumentType:  "Contract",
			Correspondent: "Петренко І. М.",
			Tags:          []string{"Договір", "Житло"},
			Date:          day(2023, time.March, 15),
			Text: `ДОГОВІР ОРЕНДИ КВАРТИРИ

Наймодавець: Петренко Ігор Миколайович. Наймач: Ковальська Олена Василівна.

1. Предмет договору
Наймодавець передає, а Наймач приймає у строкове платне користування однокімнатну квартиру загальною площею 41 квадратний метр за адресою місто Київ, вулиця Ярославів Вал.

2. Плата за користування
Розмір щомісячної плати становить 15 000 гривень. Плата вноситься не пізніше п'ятого числа кожного місяця. Комунальні послуги оплачуються Наймачем окремо за показаннями лічильників.

3. Строк
Договір укладено строком на один рік з дня підписання і може бути продовжений за письмовою згодою сторін.

4. Гарантійний платіж
Наймач сплачує гарантійний платіж у розмірі однієї місячної плати, який повертається після закінчення строку дії договору за відсутності пошкоджень.`,
		},
		{
			ID:            "uk-residence",
			User:          "u1",
			Locale:        "uk",
			Title:         "Certificate of registered residence",
			TitleOriginal: "Довідка про реєстрацію місця проживання",
			Purpose:       "Municipal certificate confirming the registered place of residence.",
			Summary:       "Certificate issued by the Kyiv district administration confirming registration at an address in the Shevchenkivskyi district since March 2023.",
			DocumentType:  "Certificate",
			Correspondent: "Шевченківська районна адміністрація",
			Tags:          []string{"Документи"},
			Date:          day(2023, time.March, 22),
			Text: `ДОВІДКА ПРО РЕЄСТРАЦІЮ МІСЦЯ ПРОЖИВАННЯ

Видана Ковальській Олені Василівні у тому, що вона зареєстрована за адресою: місто Київ, вулиця Ярославів Вал, будинок 21, квартира 7.

Дата реєстрації місця проживання: 22 березня 2023 року.

Довідка видана для подання за місцем вимоги. Відомості внесено до реєстру територіальної громади.

Начальник відділу реєстрації місця проживання, Шевченківська районна адміністрація міста Києва.`,
		},
		{
			ID:            "de-warranty",
			User:          "u1",
			Locale:        "de",
			Title:         "Washing machine warranty",
			TitleOriginal: "Garantieurkunde Waschmaschine",
			Purpose:       "Manufacturer warranty for a washing machine, with the period covered.",
			Summary:       "Three year manufacturer warranty for a washing machine bought in November 2023, covering parts and labour but not wear parts.",
			DocumentType:  "Certificate",
			Correspondent: "Weißgerät Hausgeräte",
			Tags:          []string{"Wohnung", "Garantie"},
			Date:          day(2023, time.November, 20),
			Text: `GARANTIEURKUNDE

Gerät: Waschmaschine WG-7040, Seriennummer 77-2231-04
Kaufdatum: 20. November 2023
Händler: Weißgerät Hausgeräte, Berlin

Wir gewähren auf dieses Gerät eine Garantie von drei Jahren ab Kaufdatum. Die Garantie umfasst die Behebung von Material- und Fertigungsfehlern einschließlich Ersatzteilen und Arbeitszeit.

Nicht von der Garantie erfasst sind Verschleißteile wie Dichtungen und Filter, Schäden durch unsachgemäße Aufstellung sowie Transportschäden, die nicht unverzüglich gemeldet wurden.

Im Garantiefall wenden Sie sich bitte unter Angabe der Seriennummer an den Kundendienst. Die gesetzlichen Gewährleistungsrechte gegenüber dem Verkäufer bleiben von dieser Garantie unberührt.`,
		},
		{
			ID:            "en-school",
			User:          "u1",
			Locale:        "en",
			Title:         "School enrolment confirmation",
			TitleOriginal: "School enrolment confirmation",
			Purpose:       "Confirmation that a child has a place at a primary school and when the term starts.",
			Summary:       "The school confirmed a place in year one from 2 September 2024, with an introduction morning for parents on 28 August.",
			DocumentType:  "Notice",
			Correspondent: "Lindenschule Berlin",
			Tags:          []string{"Familie"},
			Date:          day(2024, time.April, 9),
			Text: `LINDENSCHULE BERLIN
Confirmation of enrolment

Dear Ms Kowal,

We are pleased to confirm that your daughter has been allocated a place in year one at the Lindenschule for the coming school year.

The school year begins on Monday 2 September 2024. The first day starts at nine o'clock in the main hall and ends at half past eleven.

An introduction morning for parents will be held on 28 August at ten o'clock. Please bring the completed forms enclosed with this letter, together with the vaccination record.

The list of materials to be bought before the start of term is attached. Please label everything with your child's name.`,
		},
		{
			ID:            "other-owner",
			User:          "u2",
			Locale:        "de",
			Title:         "Plumber invoice for bathroom repair",
			TitleOriginal: "Rechnung Badsanierung",
			Purpose:       "Another account's plumbing invoice, present only so scoping can be checked.",
			Summary:       "A different account's invoice from the same plumbing company, with the same wording, so an evaluation can prove owner scoping still applies.",
			DocumentType:  "Invoice",
			Correspondent: "Sanitär Meier GmbH",
			Tags:          []string{"Handwerker"},
			Date:          day(2024, time.March, 12),
			Text: `Sanitär Meier GmbH

RECHNUNG Nr. 2024-0400

Die undichte Steigleitung im Badezimmer wurde freigelegt und das schadhafte Stück ersetzt. Rechnungsbetrag 842,50 EUR, zahlbar innerhalb von 14 Tagen.`,
		},
	}
}

// Cases returns the queries the evaluations are scored on, covering the ways a
// question misses its document: a different language, a different inflection, a
// typo, other words entirely, or a filter doing the work.
func Cases() []Case {
	return []Case{
		{
			Name:  "exact plumber invoice",
			Kind:  KindExact,
			Query: "Rechnung Badezimmer Steigleitung",
			Want:  []string{"de-plumber"},
		},
		{
			Name:  "exact cold rent",
			Kind:  KindExact,
			Query: "Kaltmiete Betriebskosten",
			Want:  []string{"de-lease"},
		},
		{
			Name:  "exact deductible",
			Kind:  KindExact,
			Query: "Selbstbeteiligung Schadenfall",
			Want:  []string{"de-car-insurance"},
		},
		{
			// German glues words together: "Vollkasko" is a prefix of
			// "Vollkaskoversicherung", not a token of it, and no amount of edit
			// distance bridges that. A known lexical miss, kept so the dense
			// path has something to prove.
			Name:  "morphology german compound",
			Kind:  KindMorphology,
			Query: "Vollkasko Jahresbeitrag",
			Want:  []string{"de-car-insurance"},
		},
		{
			Name:  "exact ukrainian meter reading",
			Kind:  KindExact,
			Query: "показання лічильника кВт",
			Want:  []string{"uk-electricity"},
		},
		{
			Name:  "exact english excess",
			Kind:  KindExact,
			Query: "travel excess per claim",
			Want:  []string{"en-travel-insurance"},
		},
		{
			Name:  "identifier policy number",
			Kind:  KindIdentifier,
			Query: "AB-4711",
			Want:  []string{"de-car-insurance"},
		},
		{
			Name:  "identifier invoice number",
			Kind:  KindIdentifier,
			Query: "NW-88412",
			Want:  []string{"en-hosting"},
		},
		{
			Name:  "typo in a german compound",
			Kind:  KindTypo,
			Query: "Einkommensteuerbescheed Erstattung",
			Want:  []string{"de-tax"},
		},
		{
			Name:  "typo in an english word",
			Kind:  KindTypo,
			Query: "vacination record booster",
			Want:  []string{"en-vaccination"},
		},
		{
			Name:  "typo in a name",
			Kind:  KindTypo,
			Query: "Zahnartzrechnung Krone",
			Want:  []string{"de-dentist"},
		},
		{
			Name:  "morphology genitive",
			Kind:  KindMorphology,
			Query: "Mietvertrages Kaution",
			Want:  []string{"de-lease"},
		},
		{
			Name:  "morphology plural",
			Kind:  KindMorphology,
			Query: "Garantieurkunden Verschleißteile",
			Want:  []string{"de-warranty"},
		},
		{
			Name:  "morphology ukrainian case",
			Kind:  KindMorphology,
			Query: "оренду квартири плата",
			Want:  []string{"uk-lease"},
		},
		{
			Name:  "cross language electricity",
			Kind:  KindCrossLanguage,
			Query: "electricity bill meter",
			Want:  []string{"uk-electricity"},
		},
		{
			Name:  "cross language rent kyiv",
			Kind:  KindCrossLanguage,
			Query: "apartment rental agreement Kyiv monthly",
			Want:  []string{"uk-lease"},
		},
		{
			Name:  "cross language car insurance",
			Kind:  KindCrossLanguage,
			Query: "car insurance annual premium deductible",
			Want:  []string{"de-car-insurance"},
		},
		{
			Name:  "paraphrase monthly rent",
			Kind:  KindParaphrase,
			Query: "how much rent do I pay each month for the apartment",
			Want:  []string{"de-lease", "uk-lease"},
		},
		{
			Name:  "paraphrase notice period",
			Kind:  KindParaphrase,
			Query: "how long is the notice period on my job",
			Want:  []string{"en-employment"},
		},
		{
			Name:  "paraphrase dental cost",
			Kind:  KindParaphrase,
			Query: "what did the dental crown cost me",
			Want:  []string{"de-dentist"},
		},
		{
			Name:    "filter by invoice type",
			Kind:    KindFilter,
			Query:   "Rechnung invoice",
			Want:    []string{"de-plumber", "de-dentist", "en-hosting", "uk-electricity"},
			Filters: Filters{DocumentType: "Invoice"},
		},
		{
			Name:    "filter by date range",
			Kind:    KindFilter,
			Query:   "Vertrag contract",
			Want:    []string{"de-lease"},
			Filters: Filters{DateFrom: "2022-01-01", DateTo: "2022-12-31"},
		},
		{
			Name:    "filter by tag",
			Kind:    KindFilter,
			Query:   "Versicherung insurance",
			Want:    []string{"de-car-insurance", "en-travel-insurance"},
			Filters: Filters{Tags: []string{"Versicherung"}},
		},
	}
}
