#!/usr/bin/env python3
"""Собирает каталоги строк из переводов в Scripts/l10n/<язык>.py.

    python3 Scripts/l10n/build_catalogs.py

Исходный язык — русский: ключи в коде и есть русский текст; их порядок
задаёт ru.py. Каждый <язык>.py задаёт списки UI, SEED (имена стартовых
продуктов и рецептов), DATA (категории и единицы из базы) в том же порядке
и словарь PLIST.
Значение-словарь — формы множественного числа ({"one": …, "other": …}).

Новая строка в коде → добавить её в ru.KEYS и на то же место во все
<язык>.py, затем перезапустить.
Скрипт падает, если где-то не хватает перевода или потерян %@/%lld.
"""
import importlib.util, json, re, sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
L10N = Path(__file__).resolve().parent
# Каталоги, нужные и часам, лежат в Shared/: его собирают оба таргета.
RES = ROOT / "Sources/Resources"
SHARED = ROOT / "Shared/Resources"
LANGS = ["en", "de", "fr", "es", "it", "pt-BR", "ja", "ko", "zh-Hans", "th"]
SPEC = re.compile(r"%(?:\d\$)?(?:lld|@|d)")

def load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), L10N / f"{name}.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod

ru = load("ru")
langs = {lang: load(lang) for lang in LANGS}

def unit(value):
    return {"stringUnit": {"state": "translated", "value": value}}

def entry(value, key, lang):
    if isinstance(value, dict):
        for form in value.values():
            check(key, form, lang, plural=True)
        return {"variations": {"plural": {k: unit(v) for k, v in value.items()}}}
    check(key, value, lang)
    return unit(value)

def check(key, value, lang, plural=False):
    want = sorted(s.replace("1$", "").replace("2$", "") for s in SPEC.findall(key))
    got = sorted(s.replace("1$", "").replace("2$", "") for s in SPEC.findall(value))
    # В формах множественного числа «one» вправе обойтись без числа: «One serving».
    if want != got and not (plural and not got):
        sys.exit(f"[{lang}] спецификаторы не совпадают: {key!r} → {value!r}")

def catalog(table, keys, manual=False):
    # Переводы лежат списками в порядке ключей из ru.py — так их легче сверять глазами.
    for lang, mod in langs.items():
        values = getattr(mod, table)
        if isinstance(values, list):
            if len(values) != len(keys):
                sys.exit(f"[{lang}] {table}: {len(values)} переводов на {len(keys)} ключей")
            setattr(mod, table, dict(zip(keys, values)))
    strings = {}
    for key in keys:
        locs = {}
        if key in getattr(ru, table, {}):
            locs["ru"] = entry(getattr(ru, table)[key], key, "ru")
        for lang, mod in langs.items():
            values = getattr(mod, table)
            if key not in values:
                sys.exit(f"[{lang}] нет перевода в {table}: {key!r}")
            locs[lang] = entry(values[key], key, lang)
        item = {"localizations": locs}
        if manual:
            item["extractionState"] = "manual"
        strings[key] = item
    return {"sourceLanguage": "ru", "strings": strings, "version": "1.0"}

def write(name, data, res=RES):
    path = res / name
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True) + "\n")
    print(f"{path.relative_to(ROOT)}: {len(data['strings'])} строк × {len(LANGS)} языков")

write("Localizable.xcstrings", catalog("UI", ru.KEYS), SHARED)
write("Seed.xcstrings", catalog("SEED", ru.SEED_KEYS, manual=True), SHARED)
write("Data.xcstrings", catalog("DATA", ru.DATA_KEYS, manual=True), SHARED)
for mod in langs.values():
    mod.PLIST.setdefault("CFBundleName", mod.PLIST["CFBundleDisplayName"])
write("InfoPlist.xcstrings", catalog("PLIST", list(ru.PLIST), manual=True))

# Фразы Siri: свой каталог, его ищет компилятор App Shortcuts.
phrases = load("phrases")
for lang, mod in langs.items():
    mod.PHRASES = dict(zip(phrases.KEYS, phrases.PHRASES[lang]))
    assert all("${applicationName}" in v for v in mod.PHRASES.values()), lang
write("AppShortcuts.xcstrings", catalog("PHRASES", phrases.KEYS, manual=True), SHARED)
