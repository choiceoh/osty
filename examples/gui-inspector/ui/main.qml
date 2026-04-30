import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

ApplicationWindow {
    id: root
    visible: true
    width: 1100
    height: 720
    title: "Osty Inspector"

    readonly property var state: osty.appState || ({})
    readonly property bool dark: state.theme !== "light"
    color: dark ? "#15181d" : "#f4f6f8"

    header: ToolBar {
        background: Rectangle { color: root.dark ? "#20252d" : "#ffffff" }
        RowLayout {
            anchors.fill: parent
            anchors.leftMargin: 16
            anchors.rightMargin: 16
            spacing: 12

            Label {
                text: "Osty Inspector"
                font.pixelSize: 18
                font.bold: true
                color: root.dark ? "#f4f7fb" : "#101418"
            }
            Label {
                text: root.state.status || "ready"
                color: root.dark ? "#9fb0c3" : "#53606e"
            }
            Item { Layout.fillWidth: true }
            Button {
                text: "Run"
                onClicked: osty.emit("run", { stage: stageList.currentItem ? stageList.currentItem.text : "lexer", input: sourceInput.text })
            }
            Button {
                text: "Reload"
                onClicked: osty.emit("reload", { source: "toolbar" })
            }
            Button {
                text: root.dark ? "Light" : "Dark"
                onClicked: osty.emit(root.dark ? "theme-light" : "theme-dark", {})
            }
        }
    }

    RowLayout {
        anchors.fill: parent
        anchors.margins: 16
        spacing: 16

        Frame {
            Layout.preferredWidth: 220
            Layout.fillHeight: true

            ListView {
                id: stageList
                anchors.fill: parent
                model: root.state.stages || ["lexer", "parser", "checker", "backend"]
                currentIndex: 0
                spacing: 6

                delegate: Button {
                    width: ListView.view.width
                    text: modelData
                    checkable: true
                    checked: ListView.isCurrentItem
                    onClicked: {
                        stageList.currentIndex = index
                        osty.emit("select-stage", { stage: modelData })
                    }
                }
            }
        }

        SplitView {
            Layout.fillWidth: true
            Layout.fillHeight: true
            orientation: Qt.Vertical

            Frame {
                SplitView.fillWidth: true
                SplitView.fillHeight: true

                ScrollView {
                    anchors.fill: parent
                    ColumnLayout {
                        width: parent.width
                        spacing: 12

                        TextField {
                            id: sourceInput
                            Layout.fillWidth: true
                            text: root.state.input || ""
                            placeholderText: "Osty source"
                            selectByMouse: true
                            onAccepted: osty.emit("input", { value: text })
                            onEditingFinished: osty.emit("input", { value: text })
                        }

                        Label {
                            Layout.fillWidth: true
                            text: root.state.diagnostic || ""
                            wrapMode: Text.Wrap
                            color: root.dark ? "#9fb0c3" : "#53606e"
                        }

                        TextArea {
                            Layout.fillWidth: true
                            Layout.preferredHeight: 280
                            readOnly: true
                            wrapMode: TextArea.Wrap
                            text: root.state.output || ""
                            color: root.dark ? "#e8edf3" : "#17202a"
                        }
                    }
                }
            }

            Frame {
                SplitView.fillWidth: true
                SplitView.preferredHeight: 180

                ListView {
                    anchors.fill: parent
                    model: root.state.logs || []
                    delegate: Label {
                        width: ListView.view.width
                        text: modelData
                        padding: 4
                        color: root.dark ? "#aab7c4" : "#42505e"
                    }
                }
            }
        }
    }
}
