#if canImport(HealthKit) && !targetEnvironment(macCatalyst)
import HealthKit

/// Запись съеденного в «Здоровье»: после «Приготовил» — калории и БЖУ одной
/// порции (едят обычно порцию, а не всё блюдо). Только запись: читать
/// «Здоровье» приложению незачем. Включается на каждом устройстве отдельно.
enum HealthLog {
    static let enabledKey = "healthLogEnabled"
    private static let store = HKHealthStore()

    static var isAvailable: Bool { HKHealthStore.isHealthDataAvailable() }
    static var isEnabled: Bool { UserDefaults.standard.bool(forKey: enabledKey) }

    private static let types: [(HKQuantityTypeIdentifier, HKUnit, KeyPath<Nutrition, Double>)] = [
        (.dietaryEnergyConsumed, .kilocalorie(), \.calories),
        (.dietaryProtein, .gram(), \.protein),
        (.dietaryFatTotal, .gram(), \.fat),
        (.dietaryCarbohydrates, .gram(), \.carbs),
    ]

    static func enable() async -> Bool {
        guard isAvailable else { return false }
        let share = Set(types.map { HKQuantityType($0.0) })
        let granted = (try? await store.requestAuthorization(toShare: share, read: [])) != nil
        UserDefaults.standard.set(granted, forKey: enabledKey)
        return granted
    }

    static func record(_ log: CookingLog) async {
        guard isEnabled, isAvailable else { return }
        let portion = log.nutritionPerServing
        let samples: Set<HKSample> = Set(types.compactMap { identifier, unit, keyPath in
            let value = portion[keyPath: keyPath]
            guard value > 0 else { return nil }
            return HKQuantitySample(type: HKQuantityType(identifier), quantity: HKQuantity(unit: unit, doubleValue: value),
                                    start: log.date, end: log.date)
        })
        guard !samples.isEmpty else { return }
        let food = HKCorrelation(type: HKCorrelationType(.food), start: log.date, end: log.date, objects: samples,
                                 metadata: [HKMetadataKeyFoodType: log.recipeName])
        try? await store.save(food)
    }
}
#endif
