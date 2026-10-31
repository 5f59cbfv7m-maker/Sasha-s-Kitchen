import Foundation
import Testing
@testable import FridgeOracle

@Suite("Американские единицы")
struct UnitSystemTests {
    private let us = Locale(identifier: "en_US")
    private let ru = Locale(identifier: "ru_RU")

    @Test("В США граммы — в унциях и фунтах, миллилитры — от ложек до кварт")
    func displaysUSUnits() {
        #expect(Fmt.amount(100, unit: UnitCode.gram, locale: us) == "3.5 oz")
        #expect(Fmt.amount(1000, unit: UnitCode.gram, locale: us) == "2.2 lb")
        #expect(Fmt.amount(5, unit: UnitCode.milliliter, locale: us) == "1 tsp")
        #expect(Fmt.amount(30, unit: UnitCode.milliliter, locale: us) == "2 tbsp")
        #expect(Fmt.amount(1000, unit: UnitCode.milliliter, locale: us).hasSuffix("qt"))
        #expect(Fmt.amount(250, unit: UnitCode.milliliter, locale: us).contains("cup"))
        #expect(Fmt.amount(3, unit: UnitCode.piece, locale: us) == "3 pcs")
        // КБЖУ на этикетках в США — тоже в граммах.
        #expect(Fmt.grams(11.4, locale: us) == "11.4 g")
    }

    @Test("В метрической стране всё как было")
    func keepsMetric() {
        #expect(Fmt.amount(100, unit: UnitCode.gram, locale: ru) == "100 г")
        #expect(Fmt.inputUnit(UnitCode.gram, locale: ru).base == 1)
    }

    @Test("Ввод в унциях превращается в граммы и обратно без потерь")
    func inputRoundTrip() {
        let ounce = Fmt.inputUnit(UnitCode.gram, locale: us)
        #expect(ounce.label == "oz")
        #expect(abs(16 * ounce.base - 453.592) < 0.001)
        let fluid = Fmt.inputUnit(UnitCode.milliliter, locale: us)
        #expect(fluid.label == "fl oz")
        #expect(Fmt.inputUnit(UnitCode.piece, locale: us).base == 1)
    }
}
