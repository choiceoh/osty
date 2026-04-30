import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

ApplicationWindow {
    visible: true
    width: 720
    height: 420
    title: "Osty Qt Spike"

    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 24
        spacing: 12

        Label {
            text: "Osty Qt Spike"
            font.pixelSize: 24
            font.bold: true
        }

        TextField {
            id: input
            Layout.fillWidth: true
            placeholderText: "Payload text"
            text: "hello from qml"
        }

        RowLayout {
            Button {
                text: "Emit"
                onClicked: osty.emit("run", { text: input.text })
            }
            Button {
                text: "Quit"
                onClicked: osty.emit("quit", {})
            }
        }
    }
}
