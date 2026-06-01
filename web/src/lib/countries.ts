// countries.ts — ISO-3166-1 alpha-2 → human name + flag emoji.
//
// Embedded inline so the air-gap build doesn't pull a country-name
// library off a CDN. Curated subset covering SamplePlatform's GA4
// calibration window (doc 30) plus high-RPV jurisdictions a SaaS
// customer might land on. Unknown codes — and the empty / '--'
// sentinel — fall through to UNKNOWN.

const ISO: Record<string, { name: string; flag: string }> = {
  IR: { name: 'Iran', flag: '🇮🇷' },
  US: { name: 'United States', flag: '🇺🇸' },
  DE: { name: 'Germany', flag: '🇩🇪' },
  GB: { name: 'United Kingdom', flag: '🇬🇧' },
  FR: { name: 'France', flag: '🇫🇷' },
  CA: { name: 'Canada', flag: '🇨🇦' },
  AU: { name: 'Australia', flag: '🇦🇺' },
  JP: { name: 'Japan', flag: '🇯🇵' },
  CN: { name: 'China', flag: '🇨🇳' },
  RU: { name: 'Russia', flag: '🇷🇺' },
  TR: { name: 'Turkey', flag: '🇹🇷' },
  AE: { name: 'United Arab Emirates', flag: '🇦🇪' },
  SA: { name: 'Saudi Arabia', flag: '🇸🇦' },
  IN: { name: 'India', flag: '🇮🇳' },
  PK: { name: 'Pakistan', flag: '🇵🇰' },
  AF: { name: 'Afghanistan', flag: '🇦🇫' },
  IQ: { name: 'Iraq', flag: '🇮🇶' },
  SE: { name: 'Sweden', flag: '🇸🇪' },
  NL: { name: 'Netherlands', flag: '🇳🇱' },
  IT: { name: 'Italy', flag: '🇮🇹' },
  ES: { name: 'Spain', flag: '🇪🇸' },
  PL: { name: 'Poland', flag: '🇵🇱' },
  BR: { name: 'Brazil', flag: '🇧🇷' },
  MX: { name: 'Mexico', flag: '🇲🇽' },
  ID: { name: 'Indonesia', flag: '🇮🇩' },
  KR: { name: 'South Korea', flag: '🇰🇷' },
  CH: { name: 'Switzerland', flag: '🇨🇭' },
  AT: { name: 'Austria', flag: '🇦🇹' },
  BE: { name: 'Belgium', flag: '🇧🇪' },
  DK: { name: 'Denmark', flag: '🇩🇰' },
  NO: { name: 'Norway', flag: '🇳🇴' },
  FI: { name: 'Finland', flag: '🇫🇮' },
  IE: { name: 'Ireland', flag: '🇮🇪' },
  PT: { name: 'Portugal', flag: '🇵🇹' },
  GR: { name: 'Greece', flag: '🇬🇷' },
  CZ: { name: 'Czech Republic', flag: '🇨🇿' },
  HU: { name: 'Hungary', flag: '🇭🇺' },
  RO: { name: 'Romania', flag: '🇷🇴' },
  UA: { name: 'Ukraine', flag: '🇺🇦' },
  IL: { name: 'Israel', flag: '🇮🇱' },
  EG: { name: 'Egypt', flag: '🇪🇬' },
  MA: { name: 'Morocco', flag: '🇲🇦' },
  ZA: { name: 'South Africa', flag: '🇿🇦' },
  NG: { name: 'Nigeria', flag: '🇳🇬' },
  KE: { name: 'Kenya', flag: '🇰🇪' },
  AR: { name: 'Argentina', flag: '🇦🇷' },
  CL: { name: 'Chile', flag: '🇨🇱' },
  CO: { name: 'Colombia', flag: '🇨🇴' },
  PE: { name: 'Peru', flag: '🇵🇪' },
  TH: { name: 'Thailand', flag: '🇹🇭' },
  VN: { name: 'Vietnam', flag: '🇻🇳' },
  MY: { name: 'Malaysia', flag: '🇲🇾' },
  SG: { name: 'Singapore', flag: '🇸🇬' },
  PH: { name: 'Philippines', flag: '🇵🇭' },
  NZ: { name: 'New Zealand', flag: '🇳🇿' },
};

const UNKNOWN = { name: 'Unknown', flag: '🏳️' };

export interface Country {
  name: string;
  flag: string;
}

export function lookupCountry(code: string): Country {
  if (!code || code === '--' || code === '\x00\x00') return UNKNOWN;
  const upper = code.toUpperCase();
  return ISO[upper] ?? { name: upper, flag: '🏳️' };
}
