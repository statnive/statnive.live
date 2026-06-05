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

## Empfänger und verbundene KI-Assistenten

Wir verkaufen Ihre Daten nicht und geben sie nicht an Werbenetzwerke
oder Datenbroker weiter. Der einzige Dritte, der aus Ihren Besuchen
abgeleitete Analysedaten erhalten kann, ist ein **KI-Assistent, den der
Seitenbetreiber mit seinem eigenen Dashboard verbindet**:

- Verbindet der Betreiber einen KI-Assistenten — zum Beispiel **ChatGPT
  (OpenAI, Inc., USA)** — mit seinen `statnive.live`-Analysedaten, kann
  dieser Assistent die **aggregierten** Analysedaten des Betreibers über
  eine schreibgeschützte, vom Betreiber autorisierte Verbindung lesen.
  Ihre Roh-IP-Adresse oder eine Roh-Kennung sind dabei nie enthalten
  (diese werden nicht gespeichert; siehe oben).
- Der Betreiber initiiert und autorisiert die Verbindung auf einem
  Einwilligungsbildschirm; bis dahin werden keine Analysedaten an einen
  Assistenten gesendet.
- OpenAI ist ein Empfänger in den USA; eine solche Übermittlung stützt
  sich auf den Angemessenheitsbeschluss zum EU-US Data Privacy Framework
  (Art. 45 DSGVO), mit den Garantien nach Art. 46 / 49 als Rückfallebene.
  Diese Integration ist **standardmäßig deaktiviert und gesperrt**: Sie
  ist erst aktiv, wenn ein Betreiber sie aktiviert. Die AVV
  (`/legal/dpa` § 6) regelt die OpenAI-Auftragsverarbeiterbedingungen, und
  OpenAI wird in unser öffentliches Auftragsverarbeiter-Verzeichnis
  (gespiegelt unter `https://statnive.live/privacy`) aufgenommen, bevor
  die Integration angeboten wird.

### Welche Daten ein verbundener Assistent lesen kann

Im verbundenen Zustand liest der Assistent ausschließlich diese
**aggregierten** Kategorien (dieselben wie in AVV § 3), niemals Ihre
einzelnen Rohereignisse:

- Besucher- und Seitenaufrufzahlen sowie die Aufteilung in
  wiederkehrende und neue Besucher.
- Conversions und Zielabschlüsse, einschließlich der vom Betreiber
  definierten Zielnamen.
- Umsatz und Umsatz pro Besucher, sofern der Betreiber E-Commerce
  erfasst.
- Traffic-Quelle / Kanalgruppierung und UTM-Kampagnenzuordnung.
- Ungefähre Geografie (Land, Region, Stadt) aus der einmaligen
  IP-Abfrage (siehe oben).
- Namen benutzerdefinierter Ereignisse und **Beispielwerte**
  benutzerdefinierter Eigenschaften. Beispielwerte sind Inhalte, die die
  Website des Betreibers liefert; eine fehlerhaft instrumentierte Seite
  könnte dort personenbezogene Daten ablegen, daher wird Betreibern
  geraten, niemals personenbezogene Daten in Ereigniseigenschaften zu
  speichern.

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
