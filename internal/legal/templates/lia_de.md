# Interessenabwägung (LIA) — statnive.live Analytics

> **Status:** Vorlage — vor Veröffentlichung anwaltliche Prüfung erforderlich.
>
> Dieses Dokument ist die Interessenabwägung (Legitimate Interest
> Assessment, LIA), die Betreiber einer `statnive.live`-Instanz
> vorlegen, wenn sie sich auf Art. 6 Abs. 1 lit. f DSGVO (berechtigte
> Interessen) als Rechtsgrundlage stützen. Es folgt dem dreistufigen
> Abwägungstest in EDPB Guidelines 1/2024.

## 1. Zwecktest

Der Betreiber verarbeitet pseudonyme Besuchsdaten, damit:

- Seitenaufrufe, Conversion-Trichter und Quellen-Attribution intern
  für Produkt- und Redaktionsteams verfügbar sind.
- Missbrauchsmuster (Scraper, Credential Stuffing) auf
  Ratenbegrenzer-/WAF-Ebene erkannt werden können.

Kein Werbe-Tracking, keine Profilbildung für Dritte, kein
Cross-Site-Tracking, keine Anreicherung aus externen Datenquellen.

## 2. Erforderlichkeitstest

Aggregierte Zählungen und Sitzungsrekonstruktion pro Besucher sind
für den Grundbetrieb der Website erforderlich. Weniger eingriffs-
intensive Alternativen (Server-Log-Stichproben, In-Memory-Zähler)
beantworten die für redaktionelle/produktbezogene Entscheidungen
relevanten Fragen nicht.

statnive.live minimiert von Beginn an:

- Die Roh-IP geht nur in die GeoIP-Auflösung ein und wird vor dem
  Schreiben der Zeile verworfen (CLAUDE.md Privacy Rule 1).
- Die Besucher-Identitätsspalte ist BLAKE3-128 mit einem täglich
  rotierenden Salz; das vorherige Salz wird nach Rotation gelöscht
  (Privacy Rule 8).
- Der Referrer wird ausschließlich als Host gespeichert (keine
  Query-Strings, keine Pfade).
- Kein Fingerprinting — kein Canvas/WebGL, keine Schrift-Probe,
  keine `navigator.plugins`-Enumeration (Privacy Rule 7).

## 3. Abwägungstest

| Besucherinteresse | Betreiberinteresse | Ergebnis |
|---|---|---|
| Privatsphäre des Surfverhaltens | Zählung der Seitenaufrufe | Berechtigtes Interesse überwiegt — Daten sind pseudonym, Aufbewahrung begrenzt, keine Weitergabe an Dritte. |
| Widerspruchsrecht (Art. 21) | Fortführung der Analyse | Wird über `POST /api/privacy/opt-out` gewahrt — ein zwingend erforderliches Cookie unterdrückt nachfolgende Ereignisse am Ingest-Gate. |
| Auskunftsrecht (Art. 15) | — | Wird über `GET /api/privacy/access` gewahrt (Stage 2). |
| Recht auf Löschung (Art. 17) | — | Wird über `POST /api/privacy/erase` gewahrt (Stage 2). |

## 4. Aufbewahrung

- `events_raw`-Zeilen: 180 Tage (durch ClickHouse-`TTL` durchgesetzt).
- Rollup-Tabellen (`hourly_visitors`, `daily_pages`, `daily_sources`):
  750 Tage (~24,6 Monate) — innerhalb der CNIL-Obergrenze für
  Reichweitenmessung.
- Audit-Log: append-only JSONL, rotiert nach der logrotate-Policy
  des Betreibers.

## 5. Überprüfungszyklus

Jährlich oder bei wesentlichen Änderungen der Datenerhebungs-
oberfläche der Binary (z. B. neuer Ereignistyp, neue Spalte in
`events_raw`, neuer Sub-Auftragsverarbeiter).

---

Dokument generiert von `statnive.live`. Betreiberspezifische Angaben
(Rechtsträger, Kontakt, Datenschutzbeauftragter, Sub-Auftragsverarbeiter)
sind in dieser Vorlage leer und müssen vor Veröffentlichung ergänzt
werden.
