import CloudKit
import SwiftUI
import UIKit

/// Системный экран «кто видит список»: владелец приглашает и исключает
/// людей или закрывает доступ, гость — выходит из списка.
struct CloudSharingView: UIViewControllerRepresentable {
    let share: CKShare

    func makeUIViewController(context: Context) -> UICloudSharingController {
        let controller = UICloudSharingController(share: share,
                                                  container: CKContainer(identifier: SharedLists.containerID))
        controller.availablePermissions = [.allowReadWrite, .allowPrivate]
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ controller: UICloudSharingController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(owner: share.recordID.zoneID.ownerName) }

    final class Coordinator: NSObject, UICloudSharingControllerDelegate {
        let owner: String

        init(owner: String) { self.owner = owner }

        func itemTitle(for csc: UICloudSharingController) -> String? {
            String(localized: "Список покупок")
        }

        func cloudSharingController(_ csc: UICloudSharingController, failedToSaveShareWithError error: Error) {
            print("Не удалось сохранить доступ к списку: \(error)")
        }

        func cloudSharingControllerDidStopSharing(_ csc: UICloudSharingController) {
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
