# Issue 8 review evidence

These screenshots use a synthetic invoice and a disposable local archive.

- [French review](french-review.jpg): accented supplier, grouped comma amounts entered as `1 234,56`, exact saved value `1234,56`, extracted versus corrected provenance, and a rendered preview.
- [Hebrew review](hebrew-review.jpg): mirrored desktop columns, Hebrew supplier, LTR `INV-0042`, LTR date and amounts, preserved PDF orientation, and two confirmed revisions.
- [French parsed amount](french-parse-preview.png): a grouped French amount displays the exact canonical value before confirmation. In the same browser run, `1.234` remained visible and was rejected as invalid French input.
- [Hebrew mobile details](hebrew-mobile-details.png) and [mobile preview](hebrew-mobile-preview.png): separate panels at a 375 CSS-pixel viewport.

The browser used an isolated local API, web server, mock object store, and mail catcher. The original fixture was seeded; these screenshots do not prove a browser upload. The French and Hebrew corrections were saved and visibly confirmed.

Playwright Core drove Chrome with the iPhone 13 Mini device descriptor (`isMobile`, touch, and device scale factor). At 360, 375, and 390 CSS pixels the Hebrew page had no horizontal overflow. Details and Preview switched without overlap. With the viewport height reduced to 500 pixels as a keyboard-space simulation, both actions remained in the document and Confirm could be scrolled into view. This does not simulate a real operating-system keyboard.

In the Hebrew mobile run, typing `INV-0042X` left the caret at the end of the LTR input. The date and all amount inputs had `dir="ltr"`. Tab from the invoice identifier focused Customer account reference. The unsaved-changes dialog appeared and dismissing it retained the form. A missing supplier produced a visible, localized `role="alert"` message. These DOM and interaction checks do not replace a manual screen-reader pass.
