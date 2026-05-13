# Datenschutzhinweis — statnive.live Analytics

> **Status:** Vorlage — vor Veröffentlichung anwaltliche Prüfung
> erforderlich. Betreiberspezifische Angaben (Rechtsträger,
> Kontakt, Datenschutzbeauftragter, EU-Vertreter, Sub-Auftrags-
> verarbeiter) sind in dieser Vorlage leer.

## Verantwortlicher

Verantwortlicher im Sinne der DSGVO ist der Betreiber dieser
Website — im Folgenden „wir". Wir setzen `statnive.live`, eine
datenschutzorientierte Web-Analytics-Plattform, als Auftrags-
verarbeiter ein. Die Beziehung ist über eine Auftragsverarbeitungs-
vereinbarung geregelt — siehe `/legal/dpa`.

## Welche Daten verarbeitet werden

Beim Laden einer Seite sendet Ihr Browser:

- Die Seiten-URL (Pfad, ohne Query-String).
- Die Referrer-URL (nur Host — kein Pfad, kein Query-String).
- Ihre IP-Adresse — wird **einmalig** für eine Länder-/Region-/
  Stadt-Auflösung verwendet und vor jeder Speicherung verworfen.
- Eine pseudonyme, täglich rotierende Besucher-Kennung,
  abgeleitet über BLAKE3-128 mit einem 24-stündlich rotierenden
  Salz.
- Ein First-Party-Cookie `_statnive` mit einer Besucher-UUID,
  um Mehrfach-Aufrufe innerhalb einer Sitzung zu erkennen. Wir
  speichern davon ausschließlich einen SHA-256-Hash neben Ihren
  Ereignissen.

## Was wir NICHT verarbeiten

- Kein Canvas-, WebGL- oder Schrift-Probing.
- Keine Auflistung von `navigator.plugins` oder
  `navigator.mimeTypes`.
- Keine Speicherung Ihrer Roh-IP.
- Keine Weitergabe an Werbenetzwerke.
- Keine Weitergabe an Datenbroker.

## Rechtsgrundlage

Art. 6 Abs. 1 lit. f DSGVO — berechtigtes Interesse. Unsere
Interessenabwägung ist öffentlich unter `/legal/lia` einsehbar.

## Aufbewahrungsfristen

- Rohereignis-Zeilen: 180 Tage.
- Aggregierte Tages-/Stunden-Rollups: 750 Tage (~24,6 Monate).
- Audit-Log: nach logrotate-Policy des Betreibers.

## Ihre Rechte

- **Widerspruch** (Art. 21): `POST /api/privacy/opt-out` — setzt
  ein zwingend erforderliches Cookie, das weitere Ereignisse aus
  Ihrem Browser unterdrückt.
- **Auskunft** (Art. 15): `GET /api/privacy/access` — Kopie der
  Ihrem `_statnive`-Cookie zugeordneten Ereignisse.
- **Löschung** (Art. 17): `POST /api/privacy/erase` — Löschung
  aller Ihrem `_statnive`-Cookie zugeordneten Ereignisse.

## Kontakt

Datenschutzanfragen richten Sie bitte an die auf dieser Website
angegebene Betreiber-E-Mail-Adresse. Beschwerden bei der zuständigen
Aufsichtsbehörde: die zuständige Stelle hängt von Ihrem Wohnsitz ab —
die Liste finden Sie unter
`https://edpb.europa.eu/about-edpb/board/members_en`.
