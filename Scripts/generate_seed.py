#!/usr/bin/env python3
"""Turn starter-data.md into Sources/Seed/SeedData.swift.

The markdown is the source of truth for the 49 products and 40 recipes. Re-run
this after editing it; never hand-edit the generated Swift.

    ./Scripts/generate_seed.py ../Учет\ продуктов/starter-data.md
"""
import re
import sys
import zlib
from pathlib import Path

# Recipes name their ingredients loosely; the product table is the canonical list.
ALIASES = {
    "Молоко": "Молоко 2.5%",
    "Яйца": "Яйца куриные",
    "Лук зелёный": "Зелёный лук",
    "Говяжий фарш": "Говяжий фарш (15% жирности)",
    "Сыр твёрдый": "Сыр твёрдый (голландский)",
    "Сыр Дор Блю": "Сыр Дор Блю (голубой)",
    "Сыр сливочный": "Сыр сливочный (крем-сыр)",
    "Говяжья вырезка": "Говяжья вырезка (без жира)",
    "Индейка (бедро), тушёная": "Индейка (бедро)",
}

# Стартовый холодильник: типовая упаковка и срок годности по категориям.
# Точный остаток внутри упаковки и давность покупки разбрасываются устойчивым
# хешем имени — иначе на первом экране 49 карточек с одинаковым «330 г из 600».
PACK_BY_UNIT_CATEGORY = {
    # (единица, категория): (варианты упаковки, порог «мало»)
    ("г", "Мясо"): ([400, 500, 600, 800], 200),
    ("г", "Рыба"): ([300, 400, 500], 150),
    ("г", "Морепродукты"): ([300, 400, 500], 150),
    ("г", "Молочка"): ([200, 300, 400, 500], 100),
    ("г", "Молочка/яйца"): ([300, 400], 100),
    ("г", "Крупы"): ([500, 800, 900, 1000], 200),
    ("г", "Бакалея"): ([500, 1000, 1500], 250),
    ("г", "Овощи"): ([300, 500, 700, 1000], 150),
    ("г", "Фрукты"): ([400, 600, 800], 150),
    ("г", "Зелень"): ([50, 60, 100], 20),
    ("г", "Специи"): ([500, 1000], 200),
    ("г", "Соусы"): ([250, 350, 500], 100),
    ("г", "Хлеб"): ([300, 400, 500], 120),
    ("мл", "Масла"): ([500, 900, 1000], 250),
    ("мл", "Молочка"): ([900, 1000], 250),
    ("мл", "Соусы"): ([200, 300, 500], 80),
    ("шт", "Молочка/яйца"): ([10, 20, 30], 3),
    ("шт", "Фрукты"): ([4, 6, 8], 2),
}
# Пусто в холодильнике — попадёт во вкладку «Для покупки».
EMPTY = {
    "Форель охлаждённая", "Креветки (очищенные)", "Сыр Дор Блю (голубой)",
    "Лаваш/пита", "Индейка (бедро)", "Огурцы маринованные",
}
# На исходе — ниже порога, но ещё не ноль.
LOW = {"Молоко 2.5%": 120, "Сливочное масло": 60, "Укроп": 8, "Кетчуп": 40}
SHELF_LIFE_DAYS = {
    "Мясо": 5, "Рыба": 3, "Морепродукты": 4, "Молочка": 14,
    "Молочка/яйца": 28, "Зелень": 7, "Хлеб": 6, "Овощи": 21, "Фрукты": 21,
}


def spread(name, salt, span):
    """Устойчивый разброс 0..span-1: одинаковый на любой машине и запуске."""
    return zlib.crc32((salt + name).encode("utf-8")) % span


def parse_products(text):
    products = []
    row = re.compile(r"^\|\s*(\d+)\s*\|(.+)\|\s*$")
    for line in text.splitlines():
        m = row.match(line)
        if not m:
            continue
        cells = [c.strip() for c in m.group(2).split("|")]
        if len(cells) < 7 or cells[0] in ("Продукт", "---------"):
            continue
        name, category, kcal, protein, fat, carbs, unit_raw = cells[:7]
        grams = re.search(r"~\s*([\d.]+)\s*г", unit_raw)
        unit = unit_raw.split()[0]
        products.append({
            "name": name, "category": category, "unit": unit,
            "grams_per_unit": float(grams.group(1)) if grams else 1.0,
            "kcal": float(kcal), "protein": float(protein),
            "fat": float(fat), "carbs": float(carbs),
        })
    return products


def parse_recipes(text, product_names):
    recipes = []
    current = None
    heading = re.compile(r"^###\s+\d+\.\s+(.+?)\s*$")
    meta = re.compile(r"Время:\s*(\d+)\s*мин\s*\|\s*Порций:\s*(\d+)")
    item = re.compile(r"^-\s+(.+?)\s+—\s+([\d.,]+)\s*(\S+)")
    for line in text.splitlines():
        h = heading.match(line)
        if h:
            current = {"name": h.group(1), "minutes": 0, "servings": 2, "ingredients": []}
            recipes.append(current)
            continue
        if current is None:
            continue
        m = meta.search(line)
        if m:
            current["minutes"] = int(m.group(1))
            current["servings"] = int(m.group(2))
            continue
        i = item.match(line)
        if i:
            raw = i.group(1).strip()
            name = ALIASES.get(raw, raw)
            if name not in product_names:
                sys.exit(f"Рецепт «{current['name']}»: продукт «{raw}» не найден в таблице продуктов")
            current["ingredients"].append((name, float(i.group(2).replace(",", "."))))
    return recipes


def stock_for(p):
    name, unit, category = p["name"], p["unit"], p["category"]
    packs, threshold = PACK_BY_UNIT_CATEGORY.get((unit, category), ([500], 120))
    initial = packs[spread(name, "pack", len(packs))]
    if name in EMPTY:
        current = 0.0
    elif name in LOW:
        current = float(LOW[name])
    else:
        # 30–95 % упаковки, но не ниже порога: «на исходе» задаётся явным списком.
        ratio = 0.30 + spread(name, "left", 66) / 100.0
        current = round(initial * ratio, 1)
        if current <= threshold:
            current = round(threshold * 1.4, 1)
        if current == int(current):
            current = float(int(current))
    # Покупали от 0 до 6 дней назад, но всегда в пределах срока годности:
    # стартовый холодильник не должен встречать протухшим.
    shelf = SHELF_LIFE_DAYS.get(category)
    span = 7 if shelf is None else max(1, min(7, shelf - 1))
    days_ago = spread(name, "bought", span) if current > 0 else 0
    return initial, current, threshold, days_ago


def swift_string(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def num(v):
    return str(int(v)) if float(v) == int(v) else str(v)


def main():
    source = Path(sys.argv[1] if len(sys.argv) > 1 else
                  "../Учет продуктов/starter-data.md")
    text = source.read_text(encoding="utf-8")
    products = parse_products(text)
    names = {p["name"] for p in products}
    if len(names) != len(products):
        sys.exit("В таблице продуктов есть дубликаты имён")
    recipes = parse_recipes(text, names)

    out = []
    w = out.append
    w("// Сгенерировано Scripts/generate_seed.py из starter-data.md — не править вручную.")
    w("// Продуктов: %d, рецептов: %d." % (len(products), len(recipes)))
    w("")
    w("import Foundation")
    w("")
    w("enum SeedData {")
    w("")
    w("    static let products: [SeedProduct] = [")
    for p in products:
        initial, current, threshold, days_ago = stock_for(p)
        shelf = SHELF_LIFE_DAYS.get(p["category"])
        w("        SeedProduct(name: %s, category: %s, unit: %s, gramsPerUnit: %s,"
          % (swift_string(p["name"]), swift_string(p["category"]),
             swift_string(p["unit"]), num(p["grams_per_unit"])))
        w("                    caloriesPer100: %s, proteinPer100: %s, fatPer100: %s, carbsPer100: %s,"
          % (num(p["kcal"]), num(p["protein"]), num(p["fat"]), num(p["carbs"])))
        w("                    lowStockThreshold: %s, initialAmount: %s, currentAmount: %s,"
          % (num(threshold), num(initial), num(current)))
        w("                    shelfLifeDays: %s, purchasedDaysAgo: %d),"
          % (str(shelf) if shelf else "nil", days_ago))
    w("    ]")
    w("")
    w("    static let recipes: [SeedRecipe] = [")
    for r in recipes:
        w("        SeedRecipe(name: %s, cookingTimeMinutes: %d, baseServings: %d, ingredients: ["
          % (swift_string(r["name"]), r["minutes"], r["servings"]))
        for name, amount in r["ingredients"]:
            w("            SeedIngredient(product: %s, amountPerBaseServing: %s),"
              % (swift_string(name), num(amount)))
        w("        ]),")
    w("    ]")
    w("}")
    w("")

    target = Path(__file__).resolve().parent.parent / "Sources/Seed/SeedData.swift"
    target.write_text("\n".join(out), encoding="utf-8")
    print("%s: %d продуктов, %d рецептов, %d строк ингредиентов"
          % (target.name, len(products), len(recipes),
             sum(len(r["ingredients"]) for r in recipes)))


if __name__ == "__main__":
    main()
