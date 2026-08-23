import Foundation
import Testing
@testable import FridgeOracle

@Suite("Список покупок")
struct ShoppingTests {

    @Test("В список попадает и пустое, и то, что на исходе")
    func entriesSplitByState() throws {
        let context = try makeContext(seeded: true)
        let entries = Kitchen.shoppingEntries(in: context)
        #expect(entries.contains { $0.item.name == "Форель охлаждённая" && $0.state == .out })
        #expect(entries.contains { $0.item.name == "Молоко 2.5%" && $0.state == .low })
        // Пустое — всегда выше того, что просто заканчивается.
        let firstLow = entries.firstIndex { $0.state == .low }
        let lastOut = entries.lastIndex { $0.state == .out }
        if let firstLow, let lastOut { #expect(lastOut < firstLow) }
    }

    @Test("Полный холодильник не порождает списка покупок")
    func fullFridgeIsEmptyList() throws {
        let context = try makeContext(seeded: true)
        for item in Kitchen.allStock(in: context) {
            item.currentAmount = max(item.initialAmount, 10_000)
        }
        #expect(Kitchen.shoppingEntries(in: context).isEmpty)
    }

    @Test("Текст для «Поделиться» содержит все позиции")
    func shareText() throws {
        let context = try makeContext(seeded: true)
        let entries = Kitchen.shoppingEntries(in: context)
        let text = Kitchen.shareText(for: entries)
        for entry in entries {
            #expect(text.contains(entry.item.name), "в тексте нет «\(entry.item.name)»")
        }
        #expect(text.contains("Закончилось:"))
        #expect(Kitchen.shareText(for: []).contains("пуст"))
    }

    @Test("Предложенное количество округляется до магазинного")
    func suggestionIsRounded() throws {
        let context = try makeContext(seeded: true)
        for entry in Kitchen.shoppingEntries(in: context) {
            let amount = entry.suggested
            #expect(amount > 0, "\(entry.item.name): предложено \(amount)")
            if entry.item.unit == "шт" {
                #expect(amount == amount.rounded(), "\(entry.item.name): дробные штуки")
            } else {
                #expect(amount.truncatingRemainder(dividingBy: 10) == 0,
                        "\(entry.item.name): \(amount) — не круглое число")
            }
        }
    }

    @Test("Заголовок напоминания несёт и продукт, и количество")
    func reminderTitle() throws {
        let context = try makeContext(seeded: true)
        let entry = try #require(Kitchen.shoppingEntries(in: context)
            .first { $0.item.name == "Форель охлаждённая" })
        let title = Kitchen.purchaseTitle(for: entry)
        #expect(title.hasPrefix("Форель охлаждённая — "))
        #expect(title.contains("г"))
    }
}

@Suite("Звук и форматирование")
struct SoundAndFormattingTests {

    @Test("Сгенерированный WAV имеет корректный заголовок", arguments: Feedback.Cue.allCases)
    func wavHeader(cue: Feedback.Cue) {
        let data = ToneFactory.wav(cue.notes)
        #expect(data.count > 44)
        #expect(String(decoding: data[0..<4], as: UTF8.self) == "RIFF")
        #expect(String(decoding: data[8..<12], as: UTF8.self) == "WAVE")
        #expect(String(decoding: data[36..<40], as: UTF8.self) == "data")

        let declared = data[4..<8].withUnsafeBytes { $0.loadUnaligned(as: UInt32.self) }
        #expect(Int(UInt32(littleEndian: declared)) == data.count - 8)

        let payload = data[40..<44].withUnsafeBytes { $0.loadUnaligned(as: UInt32.self) }
        #expect(Int(UInt32(littleEndian: payload)) == data.count - 44)
    }

    @Test("Числа выводятся по-русски, без хвостовых нулей")
    func numbers() {
        #expect(Fmt.number(330) == "330")
        #expect(Fmt.number(0.5) == "0,5")
        #expect(Fmt.number(247.5) == "247,5")
        #expect(Fmt.number(2.0) == "2")
        #expect(Fmt.amount(3, unit: "шт") == "3 шт")
        #expect(Fmt.kcal(1_184.8) == "1\u{00A0}185 ккал")
        #expect(Fmt.minutes(40) == "40 мин")
        #expect(Fmt.minutes(90) == "1 ч 30 мин")
    }
}
