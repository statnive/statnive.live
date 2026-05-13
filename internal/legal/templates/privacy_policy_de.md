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

## Hybrid-Einwilligung (wenn der Betreiber sie aktiviert)

Einige `statnive.live`-Bereitstellungen nutzen den **Hybrid-Modus**,
in dem die Analyse vor und nach Ihrer Einwilligung unterschiedlich
verarbeitet wird:

- **Vor Ihrer Einwilligung** (oder ohne Einwilligung): wir erheben
  ausschließlich anonyme aggregierte Zählwerte. Es wird **kein**
  `_statnive`-Identifikations-Cookie gesetzt, Ihr `Sec-GPC: 1`-Header
  wird beachtet, Zählerstände im Dashboard werden auf die nächste
  10 gerundet, und nur die drei Ereignis-Typen aus der
  Allow-Liste des Betreibers werden akzeptiert. Aus den
  gespeicherten Besucherzahlen lässt sich Ihr Einzelbesuch nicht
  rekonstruieren.
- **Nach Ihrer Einwilligung** setzen wir ein datenschutzfreundliches
  `_statnive`-Cookie (gespeichert wird ausschließlich ein
  tenant-spezifischer SHA-256-Hash; das Cookie selbst verlässt
  Ihren Browser nie als unverschlüsselter Identifier), damit
  wiederkehrende Besuche innerhalb von 13 Monaten erkennbar sind.
  Die Zählwerte werden exakt, und jeder vom Betreiber instrumentierte
  Ereignistyp wird akzeptiert.
- **Widerruf der Einwilligung** löscht beide Cookies und fügt
  Ihren Besucher der serverseitigen Suppression-Liste hinzu —
  folgende Ereignisse aus Ihrem Browser werden unbemerkt verworfen.
  Vergangene Daten werden **nicht** automatisch gelöscht; nutzen
  Sie hierfür den Löschungs-Endpunkt unten.

Einwilligung erteilen oder widerrufen Sie über
`POST /api/privacy/consent` mit dem JSON-Body
`{"action": "give"}` bzw. `{"action": "withdraw"}`. Üblicherweise
exponiert das UI des Betreibers eine Schaltfläche hierfür.

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
