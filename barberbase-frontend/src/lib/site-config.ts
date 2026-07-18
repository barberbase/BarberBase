export const BRAND = 'BarberBase';
export const LEGAL_ENTITY_NAME = 'CodeNXT Lab';
export const UDYAM_NUMBER = 'UDYAM-MH-33-0763915';
export const REGISTERED_ADDRESS = '307/B Waman Smruti, Navghar Road, Bhayandar East, Thane, MH 401105';
export const CONTACT_EMAIL = 'hello@barberbase.in';
export const CONTACT_PHONE = '+91 73040 71499';
export const SITE_URL = 'https://barberbase.in';
export const SALES_WHATSAPP = ''; // optional: a personal WhatsApp number you answer, digits only e.g. "9173..."; if empty, CTA uses mailto

// Single source of truth for Organization JSON-LD — used on both the homepage and /about
// so the two pages never drift into slightly-different schema blocks.
export const ORGANIZATION_JSON_LD = {
	'@context': 'https://schema.org',
	'@type': 'Organization',
	name: LEGAL_ENTITY_NAME,
	url: SITE_URL,
	email: CONTACT_EMAIL,
	address: {
		'@type': 'PostalAddress',
		streetAddress: '307/B Waman Smruti, Navghar Road',
		addressLocality: 'Bhayandar East',
		addressRegion: 'MH',
		postalCode: '401105',
		addressCountry: 'IN'
	}
};
