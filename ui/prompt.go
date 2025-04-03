package ui

import (
	"fmt"
	"strings"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	exec_icon          = "🚀 > "
	exec_placeholder   = "Execute something..."
	config_icon        = "🔒 > "
	config_placeholder = "Enter your OpenAI key..."
	chat_icon          = "💬 > "
	chat_placeholder   = "Ask me something..."
	bash_icon          = "$ > "          // Add this
	bash_placeholder   = "Run bash..."   // Add this
)

type Prompt struct {
	mode  PromptMode
	input textinput.Model
}

func NewPrompt(mode PromptMode) *Prompt {
	input := textinput.New()
	input.Placeholder = getPromptPlaceholder(mode)
	input.TextStyle = getPromptStyle(mode)
	input.Prompt = getPromptIcon(mode)

	if mode == ConfigPromptMode {
		input.EchoMode = textinput.EchoPassword
	}

	input.Focus()

	return &Prompt{
		mode:  mode,
		input: input,
	}
}

func (p *Prompt) GetMode() PromptMode {
	return p.mode
}

func (p *Prompt) SetMode(mode PromptMode) *Prompt {
	p.mode = mode

	p.input.TextStyle = getPromptStyle(mode)
	p.input.Prompt = getPromptIcon(mode)
	p.input.Placeholder = getPromptPlaceholder(mode)

	return p
}

func (p *Prompt) SetValue(value string) *Prompt {
	p.input.SetValue(value)

	return p
}

func (p *Prompt) GetValue() string {
	return p.input.Value()
}

func (p *Prompt) Blur() *Prompt {
	p.input.Blur()

	return p
}

func (p *Prompt) Focus() *Prompt {
	p.input.Focus()

	return p
}

func (p *Prompt) Update(msg tea.Msg) (*Prompt, tea.Cmd) {
	var updateCmd tea.Cmd
	p.input, updateCmd = p.input.Update(msg)

	return p, updateCmd
}

func (p *Prompt) View() string {
	return p.input.View()
}

func (p *Prompt) AsString() string {
	style := getPromptStyle(p.mode)

	return fmt.Sprintf("%s%s", style.Render(getPromptIcon(p.mode)), style.Render(p.input.Value()))
}

func (p *Prompt) SetPrefix(prefix string) *Prompt {
    p.input.Prompt = prefix + getPromptIcon(p.mode)
    return p
}

// SetCurrentDir is a convenience method to set the current directory in the prompt
func (p *Prompt) SetCurrentDir(dir string) *Prompt {
    // Extract just the last directory name for cleaner display
    parts := strings.Split(dir, "/")
    dirName := parts[len(parts)-1]
    if dirName == "" && len(parts) > 1 {
        dirName = parts[len(parts)-2]
    }
    
    // Set a prefix that shows the current directory
    return p.SetPrefix(fmt.Sprintf("[%s] ", dirName))
}

func getPromptStyle(mode PromptMode) lipgloss.Style {
	switch mode {
	case ExecPromptMode:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(exec_color))
	case ConfigPromptMode:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(config_color))
	case BashPromptMode:  // Add this case
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))  // Green color for bash
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(chat_color))
	}
}

func getPromptIcon(mode PromptMode) string {
	style := getPromptStyle(mode)

	switch mode {
	case ExecPromptMode:
		return style.Render(exec_icon)
	case ConfigPromptMode:
		return style.Render(config_icon)
	case BashPromptMode:  // Add this case
		return style.Render(bash_icon)
	default:
		return style.Render(chat_icon)
	}
}

// Update the getPromptPlaceholder function
func getPromptPlaceholder(mode PromptMode) string {
	switch mode {
	case ExecPromptMode:
		return exec_placeholder
	case ConfigPromptMode:
		return config_placeholder
	case BashPromptMode:  // Add this case
		return bash_placeholder
	default:
		return chat_placeholder
	}
}
