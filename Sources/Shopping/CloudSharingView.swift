import CloudKit
import SwiftUI
import UIKit

/// Системный экран «кто видит список»: владелец приглашает и исключает
/// людей или закрывает доступ, гость — выходит из списка.
struct CloudSharingView: UIViewControllerRepresentable {
    let share: CKShare
    /// Для семейной кухни — своё название и своё «закрыть доступ».
    var title = String(localized: "Список покупок")
    var onStop: (() -> Void)?

    func makeUIViewController(context: Context) -> UICloudSharingController {
        let controller = UICloudSharingController(share: share,
                                                  container: CKContainer(identifier: SharedLists.containerID))
        controller.availablePermissions = [.allowReadWrite, .allowPrivate]
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ controller: UICloudSharingController, context: Context) {}

    func makeCoordinator() -> Coordinator {
        Coordinator(owner: share.recordID.zoneID.ownerName, title: title, onStop: onStop)
    }

    final class Coordinator: NSObject, UICloudSharingControllerDelegate {
        let owner: String
        let title: String
        let onStop: (() -> Void)?

        init(owner: String, title: String, onStop: (() -> Void)?) {
            self.owner = owner
            self.title = title
            self.onStop = onStop
        }

        func itemTitle(for csc: UICloudSharingController) -> String? { title }

        func cloudSharingController(_ csc: UICloudSharingController, failedToSaveShareWithError error: Error) {
            print("Не удалось сохранить доступ к списку: \(error)")
        }

        func cloudSharingControllerDidStopSharing(_ csc: UICloudSharingController) {
            if let onStop { return onStop() }
            if owner == CKCurrentUserDefaultName {
                SharedLists.shared.stopSharing()
            } else {
                SharedLists.shared.leave(owner)
            }
        }
    }
}

extension CKShare: @retroactive Identifiable {
    public var id: CKRecord.ID { recordID }
}
