import CloudKit
import SwiftData
import SwiftUI
import WatchKit

/// Часы: список покупок — свой и общие, — чтобы отмечать купленное прямо в
/// магазине. Данные приходят через тот же iCloud, что и на iPhone; отметка
/// «купил» пополняет холодильник так же, как в приложении.
@main
struct KitchenWatchApp: App {
    @WKApplicationDelegateAdaptor private var appDelegate: WatchAppDelegate
    let container: ModelContainer

    init() {
        // Те же ключи запуска, что в приложении: `-resetData YES -storeDemo YES`
        // раскладывают кухню для скриншотов в отдельной базе без iCloud.
        let isTestRun = UserDefaults.standard.bool(forKey: "resetData")
        container = KitchenStore.makeContainer(isTestRun: isTestRun)
        if isTestRun {
            SeedLoader.reset(container.mainContext)
            if StoreDemo.isOn { StoreDemo.apply(to: container.mainContext) }
        } else {
            SharedLists.shared.start(context: container.mainContext)
        }
    }

    var body: some Scene {
        WindowGroup {
            WatchListView()
                .modifier(SharedListSync())
        }
        .modelContainer(container)
    }
}

final class WatchAppDelegate: NSObject, WKApplicationDelegate {
    func userDidAcceptCloudKitShare(with cloudKitShareMetadata: CKShare.Metadata) {
        SharedLists.accept(cloudKitShareMetadata)
    }
}
