package ui

import (
	"fmt"
	"strings"
	"bufio"
    "io"
    "os/exec"
    "sync"
	"time" // Added missing import
    "io/ioutil" // For ReadFile

	"github.com/ekkinox/yai/ai"
	"github.com/ekkinox/yai/config"
	"github.com/ekkinox/yai/history"
	"github.com/ekkinox/yai/run"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/spf13/viper"
)

// Define a BashSession struct to manage the persistent bash process
type BashSession struct {
    cmd       *exec.Cmd
    stdin     io.WriteCloser
    stdout    io.ReadCloser
    stderr    io.ReadCloser
    outputBuf string
    mutex     sync.Mutex
}

type UiState struct {
	error       error
	runMode     RunMode
	promptMode  PromptMode
	configuring bool
	querying    bool
	confirming  bool
	executing   bool
	args        string
	pipe        string
	buffer      string
	command     string
	currentDir  string // Add current directory tracking
}

type UiDimensions struct {
	width  int
	height int
}

type UiComponents struct {
	prompt   *Prompt
	renderer *Renderer
	spinner  *Spinner
}

type Ui struct {
	state      UiState
	dimensions UiDimensions
	components UiComponents
	config     *config.Config
	engine     *ai.Engine
	history    *history.History
	bashSession *BashSession // Add this field
}

func NewUi(input *UiInput) *Ui {
	ui := &Ui{
		state: UiState{
			error:       nil,
			runMode:     input.GetRunMode(),
			promptMode:  input.GetPromptMode(),
			configuring: false,
			querying:    false,
			confirming:  false,
			executing:   false,
			args:        input.GetArgs(),
			pipe:        input.GetPipe(),
			buffer:      "",
			command:     "",
			currentDir:  "", // Initialize current directory
		},
		dimensions: UiDimensions{
			150,
			150,
		},
		components: UiComponents{
			prompt: NewPrompt(input.GetPromptMode()),
			renderer: NewRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(150),
			),
			spinner: NewSpinner(),
		},
		history: history.NewHistory(),
	}
	// Initialize bash session
    ui.initBashSession()

	return ui
}

func (u *Ui) initBashSession() {
    // Create a bash process with login shell to load profile
    cmd := exec.Command("bash", "--login")
    
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()
    
    // Start the process
    cmd.Start()
    
    u.bashSession = &BashSession{
        cmd:       cmd,
        stdin:     stdin,
        stdout:    stdout,
        stderr:    stderr,
        outputBuf: "",
        mutex:     sync.Mutex{},
    }
    
    // Start a goroutine to read stdout
    go func() {
        scanner := bufio.NewScanner(stdout)
        for scanner.Scan() {
            line := scanner.Text()
            
            u.bashSession.mutex.Lock()
            u.bashSession.outputBuf += line + "\n"
            u.bashSession.mutex.Unlock()
        }
    }()
    
    // Start a goroutine to read stderr
    go func() {
        scanner := bufio.NewScanner(stderr)
        for scanner.Scan() {
            line := scanner.Text()
            
            u.bashSession.mutex.Lock()
            u.bashSession.outputBuf += line + "\n"
            u.bashSession.mutex.Unlock()
        }
    }()
}

// Execute a command in the persistent bash session
func (u *Ui) execInBashSession(input string) tea.Cmd {
    u.state.querying = false
    u.state.confirming = false
    u.state.executing = true

    return func() tea.Msg {
        // Clear previous output
        u.bashSession.mutex.Lock()
        u.bashSession.outputBuf = ""
        u.bashSession.mutex.Unlock()
        
        // Create a unique marker to detect command completion
        marker := fmt.Sprintf("YAI_CMD_COMPLETE_%d", time.Now().UnixNano())
        
        // Write command to stdin with completion marker
        cmdWithMarker := fmt.Sprintf("%s; echo '%s'", input, marker)
        _, err := io.WriteString(u.bashSession.stdin, cmdWithMarker+"\n")
        if err != nil {
            u.state.executing = false
            return run.NewRunOutput(err, fmt.Sprintf("[error: %v]", err), "")
        }
        
        // Wait for the marker to appear in the output
        startTime := time.Now()
        timeout := 30 * time.Second // Maximum wait time
        
        for {
            // Check if we've exceeded the timeout
            if time.Since(startTime) > timeout {
                u.state.executing = false
                return run.NewRunOutput(fmt.Errorf("command timed out"), "[error: command timed out]", "")
            }
            
            // Get the current output
            u.bashSession.mutex.Lock()
            output := u.bashSession.outputBuf
            u.bashSession.mutex.Unlock()
            
            // Check if the marker is in the output
            if strings.Contains(output, marker) {
                // Remove the marker from the output
                cleanOutput := strings.Replace(output, marker, "", -1)
                cleanOutput = strings.Replace(cleanOutput, "\n\n", "\n", -1) // Clean up extra newlines
                
                u.state.executing = false
                u.state.command = ""
                
                return run.NewRunOutput(nil, "", cleanOutput)
            }
            
            // Sleep a bit before checking again
            time.Sleep(50 * time.Millisecond)
        }
    }
}

// Clean up the bash session when the application exits
func (u *Ui) cleanupBashSession() {
    if u.bashSession != nil && u.bashSession.cmd != nil && u.bashSession.cmd.Process != nil {
        // Send exit command
        io.WriteString(u.bashSession.stdin, "exit\n")
        // Wait for process to exit
        u.bashSession.cmd.Wait()
    }
}

func (u *Ui) getBashEnvironment() map[string]string {
    // Execute env command to get all variables
    u.execInBashSession("env > /tmp/yai_env.txt")
    
    // Read the file
    data, err := ioutil.ReadFile("/tmp/yai_env.txt")
    if err != nil {
        return nil
    }
    
    // Parse environment variables
    env := make(map[string]string)
    lines := strings.Split(string(data), "\n")
    for _, line := range lines {
        parts := strings.SplitN(line, "=", 2)
        if len(parts) == 2 {
            env[parts[0]] = parts[1]
        }
    }
    
    return env
}

// Modify the AI engine to be aware of bash environment
func (u *Ui) updateEngineWithBashEnv() {
    env := u.getBashEnvironment()
    if env != nil {
        // Create a string representation of the environment
        envStr := "Current bash environment variables:\n"
        for k, v := range env {
            envStr += fmt.Sprintf("%s=%s\n", k, v)
        }
        
        // Update the engine with this information
        u.engine.SetBashEnvironment(envStr)
    }
}

func (u *Ui) Init() tea.Cmd {
	config, err := config.NewConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			if u.state.runMode == ReplMode {
				return tea.Sequence(
					tea.ClearScreen,
					u.startConfig(),
				)
			} else {
				return u.startConfig()
			}
		} else {
			return tea.Sequence(
				tea.Println(u.components.renderer.RenderError(err.Error())),
				tea.Quit,
			)
		}
	}

	if u.state.runMode == ReplMode {
		return u.startRepl(config)
	} else {
		return u.startCli(config)
	}
}

func (u *Ui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds       []tea.Cmd
		promptCmd  tea.Cmd
		spinnerCmd tea.Cmd
	)

	switch msg := msg.(type) {
	// spinner
	case spinner.TickMsg:
		if u.state.querying {
			u.components.spinner, spinnerCmd = u.components.spinner.Update(msg)
			cmds = append(
				cmds,
				spinnerCmd,
			)
		}
	// size
	case tea.WindowSizeMsg:
		u.dimensions.width = msg.Width
		u.dimensions.height = msg.Height
		u.components.renderer = NewRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(u.dimensions.width),
		)
	// keyboard
	case tea.KeyMsg:
		switch msg.Type {
		// quit
		case tea.KeyCtrlC:
			u.cleanupBashSession() // Clean up bash session
			return u, tea.Quit
		// history
		case tea.KeyUp, tea.KeyDown:
			if !u.state.querying && !u.state.confirming {
				var input *string
				if msg.Type == tea.KeyUp {
					input = u.history.GetPrevious()
				} else {
					input = u.history.GetNext()
				}
				if input != nil {
					u.components.prompt.SetValue(*input)
					u.components.prompt, promptCmd = u.components.prompt.Update(msg)
					cmds = append(
						cmds,
						promptCmd,
					)
				}
			}
		// switch mode
		case tea.KeyCtrlQ:
			if !u.state.querying && !u.state.confirming {
				// Cycle through modes: Exec -> Chat -> Bash -> Exec
				u.updateCurrentDir()
				switch u.state.promptMode {
				case ExecPromptMode:
					u.state.promptMode = ChatPromptMode
					u.components.prompt.SetMode(ChatPromptMode)
					u.engine.SetMode(ai.ChatEngineMode)
				case ChatPromptMode:
					u.state.promptMode = BashPromptMode
					u.components.prompt.SetMode(BashPromptMode)
					// No need to change engine mode for bash as we'll execute directly
				case BashPromptMode:
					u.state.promptMode = ExecPromptMode
					u.components.prompt.SetMode(ExecPromptMode)
					u.engine.SetMode(ai.ExecEngineMode)
				default:
					u.state.promptMode = ExecPromptMode
					u.components.prompt.SetMode(ExecPromptMode)
					u.engine.SetMode(ai.ExecEngineMode)
				}
				u.engine.Reset()
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					textinput.Blink,
				)
			}
		// enter
		case tea.KeyEnter:
			if u.state.configuring {
				return u, u.finishConfig(u.components.prompt.GetValue())
			}
			if !u.state.querying && !u.state.confirming {
				input := u.components.prompt.GetValue()
				if input != "" {
					inputPrint := u.components.prompt.AsString()
					u.history.Add(input)
					u.components.prompt.SetValue("")
					u.components.prompt, promptCmd = u.components.prompt.Update(msg)
					
					// Handle different prompt modes
					if u.state.promptMode == ChatPromptMode {
						cmds = append(
							cmds,
							promptCmd,
							tea.Println(inputPrint),
							u.startChatStream(input),
							u.awaitChatStream(),
						)
					} else if u.state.promptMode == BashPromptMode {
						// Direct bash execution
						cmds = append(
							cmds,
							promptCmd,
							tea.Println(inputPrint),
							u.execInBashSession(input), // Use the persistent bash session
							u.updateCurrentDir(),      // Update current directory after command execution
						)
					} else {
						// Default exec mode (AI-assisted)
						cmds = append(
							cmds,
							promptCmd,
							tea.Println(inputPrint),
							u.startExec(input),
							u.components.spinner.Tick,
						)
					}
				}
			}

		// help
		case tea.KeyCtrlH:
			if !u.state.configuring && !u.state.querying && !u.state.confirming {
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					tea.Println(u.components.renderer.RenderContent(u.components.renderer.RenderHelpMessage())),
					textinput.Blink,
				)
			}

		// clear
		case tea.KeyCtrlL:
			if !u.state.querying && !u.state.confirming {
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					tea.ClearScreen,
					textinput.Blink,
				)
			}

		// reset
		case tea.KeyCtrlR:
			if !u.state.querying && !u.state.confirming {
				u.history.Reset()
				u.engine.Reset()
				u.components.prompt.SetValue("")
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					tea.ClearScreen,
					textinput.Blink,
				)
			}

		// edit settings
		case tea.KeyCtrlS:
			if !u.state.querying && !u.state.confirming && !u.state.configuring && !u.state.executing {
				u.state.executing = true
				u.state.buffer = ""
				u.state.command = ""
				u.components.prompt.Blur()
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					u.editSettings(),
				)
			}

		default:
			if u.state.confirming {
				if strings.ToLower(msg.String()) == "y" {
					u.state.confirming = false
					u.state.executing = true
					u.state.buffer = ""
					u.components.prompt.SetValue("")
					return u, tea.Sequence(
						promptCmd,
						// u.execCommand(u.state.command),
						// u.execCommand("sleep 0.001"),
						u.execInBashSession(u.state.command),
					)
				} else {
					u.state.confirming = false
					u.state.executing = false
					u.state.buffer = ""
					u.components.prompt, promptCmd = u.components.prompt.Update(msg)
					u.components.prompt.SetValue("")
					u.components.prompt.Focus()
					if u.state.runMode == ReplMode {
						cmds = append(
							cmds,
							promptCmd,
							tea.Println(fmt.Sprintf("\n%s\n", u.components.renderer.RenderWarning("[cancel]"))),
							textinput.Blink,
						)
					} else {
						return u, tea.Sequence(
							promptCmd,
							tea.Println(fmt.Sprintf("\n%s\n", u.components.renderer.RenderWarning("[cancel]"))),
							tea.Quit,
						)
					}
				}
				u.state.command = ""
			} else {
				u.components.prompt.Focus()
				u.components.prompt, promptCmd = u.components.prompt.Update(msg)
				cmds = append(
					cmds,
					promptCmd,
					textinput.Blink,
				)
			}
		}
	// engine exec feedback
	case ai.EngineExecOutput:
		var output string
		if msg.IsExecutable() {
			u.state.confirming = true
			u.state.command = msg.GetCommand()
			output = u.components.renderer.RenderContent(fmt.Sprintf("`%s`", u.state.command))
			output += fmt.Sprintf("  %s\n\n  confirm execution? [y/N]", u.components.renderer.RenderHelp(msg.GetExplanation()))
			u.components.prompt.Blur()
		} else {
			output = u.components.renderer.RenderContent(msg.GetExplanation())
			u.components.prompt.Focus()
			if u.state.runMode == CliMode {
				return u, tea.Sequence(
					tea.Println(output),
					tea.Quit,
				)
			}
		}
		u.components.prompt, promptCmd = u.components.prompt.Update(msg)
		return u, tea.Sequence(
			promptCmd,
			textinput.Blink,
			tea.Println(output),
		)
	// engine chat stream feedback
	case ai.EngineChatStreamOutput:
		if msg.IsLast() {
			output := u.components.renderer.RenderContent(u.state.buffer)
			u.state.buffer = ""
			u.components.prompt.Focus()
			if u.state.runMode == CliMode {
				return u, tea.Sequence(
					tea.Println(output),
					tea.Quit,
				)
			} else {
				return u, tea.Sequence(
					tea.Println(output),
					textinput.Blink,
				)
			}
		} else {
			return u, u.awaitChatStream()
		}
	// runner feedback
	case run.RunOutput:
		u.state.querying = false
		u.components.prompt, promptCmd = u.components.prompt.Update(msg)
		u.components.prompt.Focus()
		output := u.components.renderer.RenderSuccess(fmt.Sprintf("\n%s\n", msg.GetSuccessMessage()))
		if msg.HasError() {
			output = u.components.renderer.RenderError(fmt.Sprintf("\n%s\n", msg.GetErrorMessage()))
		}
		if u.state.runMode == CliMode {
			return u, tea.Sequence(
				tea.Println(output),
				tea.Quit,
			)
		} else {
			return u, tea.Sequence(
				tea.Println(output),
				promptCmd,
				textinput.Blink,
			)
		}
	// errors
	case error:
		u.state.error = msg
		return u, nil
	}

	return u, tea.Batch(cmds...)
}

// Update the current directory from bash session
func (u *Ui) updateCurrentDir() tea.Cmd {
    return func() tea.Msg {
        // Execute pwd command
        u.execInBashSession("pwd > /tmp/yai_pwd.txt")
        
        // Give it a moment to complete
        time.Sleep(50 * time.Millisecond)
        
        // Read the current directory
        data, err := ioutil.ReadFile("/tmp/yai_pwd.txt")
        if err != nil {
            return nil
        }
        
        pwd := strings.TrimSpace(string(data))
        u.state.currentDir = pwd
        
        // Update the prompt prefix to show current directory
        u.updatePromptPrefix()
        
        return nil
    }
}

func (u *Ui) updatePromptPrefix() {
    if u.state.currentDir != "" {
        // Extract just the last directory name for cleaner display
        parts := strings.Split(u.state.currentDir, "/")
        dirName := parts[len(parts)-1]
        if dirName == "" && len(parts) > 1 {
            dirName = parts[len(parts)-2]
        }
        
        // Set a prefix that shows the current directory
        prefix := fmt.Sprintf("[%s] ", dirName)
        u.components.prompt.SetPrefix(prefix)
    }
}

func (u *Ui) View() string {
	if u.state.error != nil {
		return u.components.renderer.RenderError(fmt.Sprintf("[error] %s", u.state.error))
	}

	if u.state.configuring {
		return fmt.Sprintf(
			"%s\n%s",
			u.components.renderer.RenderContent(u.state.buffer),
			u.components.prompt.View(),
		)
	}

	if !u.state.querying && !u.state.confirming && !u.state.executing {
		return u.components.prompt.View()
	}

	if u.state.promptMode == ChatPromptMode {
		return u.components.renderer.RenderContent(u.state.buffer)
	} else {
		if u.state.querying {
			return u.components.spinner.View()
		} else {
			if !u.state.executing {
				return u.components.renderer.RenderContent(u.state.buffer)
			}
		}
	}

	return ""
}

func (u *Ui) startRepl(config *config.Config) tea.Cmd {
    return tea.Sequence(
        tea.ClearScreen,
        tea.Println(u.components.renderer.RenderContent(u.components.renderer.RenderHelpMessage())),
        textinput.Blink,
        func() tea.Msg {
            u.config = config

            if u.state.promptMode == DefaultPromptMode {
                u.state.promptMode = GetPromptModeFromString(config.GetUserConfig().GetDefaultPromptMode())
            }

            engineMode := ai.ExecEngineMode
            if u.state.promptMode == ChatPromptMode {
                engineMode = ai.ChatEngineMode
            }
            // Note: BashPromptMode doesn't need a special engine mode as it executes commands directly

            engine, err := ai.NewEngine(engineMode, config)
            if err != nil {
                return err
            }

            if u.state.pipe != "" {
                engine.SetPipe(u.state.pipe)
            }

            u.engine = engine
            u.state.buffer = "Welcome \n\n"
            u.state.command = ""
            u.components.prompt = NewPrompt(u.state.promptMode)

            return nil
        },
    )
}

func (u *Ui) startCli(config *config.Config) tea.Cmd {
    u.config = config

    if u.state.promptMode == DefaultPromptMode {
        u.state.promptMode = GetPromptModeFromString(config.GetUserConfig().GetDefaultPromptMode())
    }

    engineMode := ai.ExecEngineMode
    if u.state.promptMode == ChatPromptMode {
        engineMode = ai.ChatEngineMode
    }

    engine, err := ai.NewEngine(engineMode, config)
    if err != nil {
        u.state.error = err
        return nil
    }

    if u.state.pipe != "" {
        engine.SetPipe(u.state.pipe)
    }

    u.engine = engine
    u.state.querying = true
    u.state.confirming = false
    u.state.buffer = ""
    u.state.command = ""

    // Handle different prompt modes for CLI
    if u.state.promptMode == BashPromptMode {
        // Execute bash command directly without confirmation
        return func() tea.Msg {
            u.state.querying = false
            u.state.executing = true
            return u.execInBashSession(u.state.args)() // Use bash session instead
        }
    } else if u.state.promptMode == ExecPromptMode {
        return tea.Batch(
            u.components.spinner.Tick,
            func() tea.Msg {
                output, err := u.engine.ExecCompletion(u.state.args)
                u.state.querying = false
                if err != nil {
                    return err
                }

                return *output
            },
        )
    } else {
        return tea.Batch(
            u.startChatStream(u.state.args),
            u.awaitChatStream(),
        )
    }
}

func (u *Ui) execBashCommand(input string) tea.Cmd {
    u.state.querying = false
    u.state.confirming = false
    u.state.executing = true

    c := run.PrepareInteractiveCommand(input)

    return tea.ExecProcess(c, func(error error) tea.Msg {
        u.state.executing = false
        u.state.command = ""

        if error != nil {
            return run.NewRunOutput(error, fmt.Sprintf("[error: %v]", error), "")
        }
        return run.NewRunOutput(nil, "", "[command completed]")
    })
}



func (u *Ui) startConfig() tea.Cmd {
	return func() tea.Msg {
		u.state.configuring = true
		u.state.querying = false
		u.state.confirming = false
		u.state.executing = false

		u.state.buffer = u.components.renderer.RenderConfigMessage()
		u.state.command = ""
		u.components.prompt = NewPrompt(ConfigPromptMode)

		return nil
	}
}

func (u *Ui) finishConfig(key string) tea.Cmd {
	u.state.configuring = false

	config, err := config.WriteConfig(key, true)
	if err != nil {
		u.state.error = err
		return nil
	}

	u.config = config
	engine, err := ai.NewEngine(ai.ExecEngineMode, config)
	if err != nil {
		u.state.error = err
		return nil
	}

	if u.state.pipe != "" {
		engine.SetPipe(u.state.pipe)
	}

	u.engine = engine

	if u.state.runMode == ReplMode {
		return tea.Sequence(
			tea.ClearScreen,
			tea.Println(u.components.renderer.RenderSuccess("\n[settings ok]\n")),
			textinput.Blink,
			func() tea.Msg {
				u.state.buffer = ""
				u.state.command = ""
				u.components.prompt = NewPrompt(ExecPromptMode)

				return nil
			},
		)
	} else {
		if u.state.promptMode == ExecPromptMode {
			u.state.querying = true
			u.state.configuring = false
			u.state.buffer = ""
			return tea.Sequence(
				tea.Println(u.components.renderer.RenderSuccess("\n[settings ok]")),
				u.components.spinner.Tick,
				func() tea.Msg {
					output, err := u.engine.ExecCompletion(u.state.args)
					u.state.querying = false
					if err != nil {
						return err
					}

					return *output
				},
			)
		} else {
			return tea.Batch(
				u.startChatStream(u.state.args),
				u.awaitChatStream(),
			)
		}
	}
}

func (u *Ui) startExec(input string) tea.Cmd {
	return func() tea.Msg {
		u.state.querying = true
		u.state.confirming = false
		u.state.buffer = ""
		u.state.command = ""

		output, err := u.engine.ExecCompletion(input)
		u.state.querying = false
		if err != nil {
			return err
		}

		return *output
	}
}

func (u *Ui) startChatStream(input string) tea.Cmd {
	return func() tea.Msg {
		u.state.querying = true
		u.state.executing = false
		u.state.confirming = false
		u.state.buffer = ""
		u.state.command = ""

		err := u.engine.ChatStreamCompletion(input)
		if err != nil {
			return err
		}

		return nil
	}
}

func (u *Ui) awaitChatStream() tea.Cmd {
	return func() tea.Msg {
		output := <-u.engine.GetChannel()
		u.state.buffer += output.GetContent()
		u.state.querying = !output.IsLast()

		return output
	}
}

func (u *Ui) execCommand(input string) tea.Cmd {
	u.state.querying = false
	u.state.confirming = false
	u.state.executing = true

	c :=  run.PrepareInteractiveCommand(input)

	return tea.ExecProcess(c, func(error error) tea.Msg {
		u.state.executing = false
		u.state.command = ""

		return run.NewRunOutput(error, "[error]", "[ok]")
	})
}

func (u *Ui) editSettings() tea.Cmd {
	u.state.querying = false
	u.state.confirming = false
	u.state.executing = true

	c := run.PrepareEditSettingsCommand(fmt.Sprintf(
		"%s %s",
		u.config.GetSystemConfig().GetEditor(),
		u.config.GetSystemConfig().GetConfigFile(),
	))

	return tea.ExecProcess(c, func(error error) tea.Msg {
		u.state.executing = false
		u.state.command = ""

		if error != nil {
			return run.NewRunOutput(error, "[settings error]", "")
		}

		config, error := config.NewConfig()
		if error != nil {
			return run.NewRunOutput(error, "[settings error]", "")
		}

		u.config = config
		engine, error := ai.NewEngine(ai.ExecEngineMode, config)
		if u.state.pipe != "" {
			engine.SetPipe(u.state.pipe)
		}
		if error != nil {
			return run.NewRunOutput(error, "[settings error]", "")
		}
		u.engine = engine

		return run.NewRunOutput(nil, "", "[settings ok]")
	})
}
