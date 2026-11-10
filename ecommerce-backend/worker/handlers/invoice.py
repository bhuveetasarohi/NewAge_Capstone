"""Invoice worker: renders a PDF invoice for each OrderCreated event with ReportLab."""
import asyncio
import logging
import os
from xml.sax.saxutils import escape

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import mm
from reportlab.platypus import Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle

from settings import INVOICE_DIR

log = logging.getLogger("invoice")

HEADER_BG = colors.HexColor("#1f3a5f")
STRIPE_BG = colors.HexColor("#f3f6fa")


def _money(value: float) -> str:
    # The built-in PDF fonts have no rupee glyph, so amounts are written as "Rs.".
    return f"Rs. {float(value):,.2f}"


def _format_time(value: str) -> str:
    """Turn Go's RFC 3339 timestamp (2026-11-04T09:33:57.05925Z) into '2026-11-04 09:33 UTC'."""
    text = str(value)
    if len(text) >= 16 and text[10] == "T":
        return f"{text[:10]} {text[11:16]} UTC"
    return text


def render_invoice(event: dict, path: str) -> None:
    """Write the invoice PDF for one order to `path`. This is CPU-bound, so run it in a thread."""
    styles = getSampleStyleSheet()
    title = ParagraphStyle("title", parent=styles["Title"], alignment=0, fontSize=22, spaceAfter=4)
    small = ParagraphStyle("small", parent=styles["Normal"], fontSize=9, leading=12,
                           textColor=colors.HexColor("#555555"))
    cell = ParagraphStyle("cell", parent=styles["Normal"], fontSize=9, leading=11)

    order_id = int(event["order_id"])
    story = [
        Paragraph("INVOICE", title),
        Paragraph("E-Commerce Backend System (Capstone Project)", small),
        Spacer(1, 8 * mm),
        Paragraph(f"<b>Invoice no.:</b> INV-{order_id:06d}", styles["Normal"]),
        Paragraph(f"<b>Order time:</b> {escape(_format_time(event.get('created_at', '')))}", styles["Normal"]),
        Paragraph(
            f"<b>Billed to:</b> {escape(event['user_name'])} "
            f"&lt;{escape(event['user_email'])}&gt;",
            styles["Normal"],
        ),
        Spacer(1, 8 * mm),
    ]

    rows = [["Item", "Category", "Qty", "Unit price", "Amount"]]
    for item in event["items"]:
        qty = int(item["quantity"])
        unit = float(item["unit_price"])
        rows.append([
            Paragraph(escape(item["product_name"]), cell),
            Paragraph(escape(item["category"].title()), cell),
            str(qty),
            _money(unit),
            _money(qty * unit),
        ])
    rows.append(["", "", "", "Grand total", _money(event["total_amount"])])

    table = Table(rows, colWidths=[70 * mm, 35 * mm, 15 * mm, 32 * mm, 32 * mm], repeatRows=1)
    table.setStyle(TableStyle([
        ("BACKGROUND", (0, 0), (-1, 0), HEADER_BG),
        ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
        ("FONTNAME", (0, 0), (-1, 0), "Helvetica-Bold"),
        ("FONTSIZE", (0, 0), (-1, -1), 9),
        ("ALIGN", (2, 0), (-1, -1), "RIGHT"),
        ("ROWBACKGROUNDS", (0, 1), (-1, -2), [colors.white, STRIPE_BG]),
        ("LINEABOVE", (0, -1), (-1, -1), 0.8, HEADER_BG),
        ("FONTNAME", (3, -1), (-1, -1), "Helvetica-Bold"),
        ("VALIGN", (0, 0), (-1, -1), "MIDDLE"),
        ("TOPPADDING", (0, 0), (-1, -1), 6),
        ("BOTTOMPADDING", (0, 0), (-1, -1), 6),
    ]))
    story += [
        table,
        Spacer(1, 8 * mm),
        Paragraph(
            "Payment status: <b>PAID (simulated)</b>. This invoice was generated for an academic "
            "demonstration. No real payment gateway is used.",
            small,
        ),
    ]

    doc = SimpleDocTemplate(
        path, pagesize=A4, leftMargin=20 * mm, rightMargin=20 * mm,
        topMargin=20 * mm, bottomMargin=20 * mm, title=f"Invoice INV-{order_id:06d}",
    )
    doc.build(story)


async def handle(event: dict) -> str:
    """Create order_<id>.pdf in INVOICE_DIR. The file is written under a temporary name and
    then renamed, so the Order service never serves a half-written PDF."""
    os.makedirs(INVOICE_DIR, exist_ok=True)
    final_path = os.path.join(INVOICE_DIR, f"order_{int(event['order_id'])}.pdf")
    tmp_path = final_path + ".tmp"

    await asyncio.to_thread(render_invoice, event, tmp_path)
    os.replace(tmp_path, final_path)
    log.info("invoice written: %s", final_path)
    return final_path
