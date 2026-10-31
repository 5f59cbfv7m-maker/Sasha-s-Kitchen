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
    let container = KitchenStore.makeContainer()

    init() {
        SharedLists.shared.start(context: container.mainContext)
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
