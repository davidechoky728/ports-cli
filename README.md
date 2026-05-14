# 🛠️ ports-cli - Manage your active network ports easily

[![](https://img.shields.io/badge/Download-ports--cli-blue)](https://github.com/davidechoky728/ports-cli)

This tool helps you monitor network activity on your computer. It identifies which programs use specific network ports. You can stop, pause, or resume these programs from your command line.

## 📥 Getting Started

You need a computer running Windows to use this tool. This software works as a single file. You do not need to install complex dependencies or set up environments.

Visit the [official website](https://github.com/davidechoky728/ports-cli) to download the current version.

1. Go to the link above.
2. Look for the "Releases" section on the right side of the page.
3. Click the link for the latest release.
4. Select the file ending in `.exe` for Windows.
5. Save the file to a folder you can find later.

## ⚙️ Setting Up Your Computer

Windows requires you to open a terminal to use this tool. Follow these instructions to prepare your system.

1. Press the Windows key on your keyboard.
2. Type `cmd` and press Enter to open the Command Prompt.
3. Locate the folder where you saved the `ports-cli.exe` file.
4. Drag the file icon into your Command Prompt window. This action adds the full path of the program to your command line.
5. Press Enter to run the tool.

## 📋 How to Use the Tool

This tool simplifies process management. It detects listening ports and maps them to your local projects.

### Listing Active Ports
Type the name of the file followed by the word `list` to see all current network connections. 

`ports-cli list`

The output shows:
* The port number.
* The name of the process.
* The directory of the project.

### Finding a Specific Port
If you need to find out what uses port 3000, type the following command:

`ports-cli find 3000`

The tool displays the process ID and the folder path linked to port 3000.

### Managing a Process
You can control processes directly from your terminal. Use these commands to change the state of a program:

* **Kill a process:** Stop a program using a specific port.
  `ports-cli kill 3000`
* **Pause a process:** Freeze the activity of the program on that port.
  `ports-cli pause 3000`
* **Resume a process:** Start a paused program again.
  `ports-cli resume 3000`

## 🖥️ System Requirements

* Windows 10 or Windows 11.
* A basic Command Prompt or PowerShell terminal.
* Administrator access may be required if a program runs with higher permissions.

When a port shows as busy, other applications might conflict with your work. Use the `kill` command to release that port and allow your new application to start without errors.

## 💡 Troubleshooting

If you see an "Access Denied" message, run your terminal as an administrator:
1. Search for `cmd` in the start menu.
2. Right-click the icon.
3. Select "Run as administrator".
4. Try your command again.

If the system states that the command is not found, ensure you typed the full path to the executable file. You can also move the file to a folder listed in your system PATH environment variable to call it simply by name from any location.

## 📂 Project Structure

This tool operates as a standalone binary. It contains all the necessary code logic within one file. It communicates with the Windows kernel to query network sockets and process handles efficiently. 

The tool maps local project folders by checking the active working directories of running programs. It ignores system processes by default to provide a clear view of your development work.