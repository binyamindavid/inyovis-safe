# Synthetic digital invoice fixtures

These PDFs contain invented invoice data only. `digital_columns_en.pdf` uses a billing-address column and a separate `Invoice Number: INV00001234` column on the same line. Its USD amounts use labels and dollar signs outside the current EUR/ILS review contract. The expected extraction is the printed invoice number only.

`digital_bidi_he.pdf` embeds Noto Sans Hebrew and includes bidi formatting controls. Poppler's `pdftotext -layout` emits `00001234` before a reversed Hebrew invoice label, and `₪ 100.00` and `₪ 117.00` after reversed subtotal and grand-total labels. The expected candidates are invoice number `00001234`, subtotal `100.00`, grand total `117.00`, and currency `ILS`.

Tests run `pdftotext -layout` on both PDFs before parsing them through the production PDF path. No user invoice was used to make these fixtures.
