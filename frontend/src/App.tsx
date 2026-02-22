import Topbar from "./components/Topbar"
import { useState, useEffect } from "react";
import ClientPage from "./pages/ClientPage";
import ServerPage from "./pages/ServerPage";
import SettingsPage from "./pages/SettingsPage";
import { Radio, Server, Settings } from "lucide-react";
import { cn } from "@/lib/utils";
import { Toaster } from "@/components/ui/toaster";
import { GetLastActiveMode, SaveLastActiveMode } from "../wailsjs/go/main/App";

function App() {
  const [activeTab, setActiveTab] = useState<"receiver" | "sender" | "settings">("receiver");

  // Load the last active mode on startup
  useEffect(() => {
    GetLastActiveMode().then((mode) => {
      if (mode === "receiver" || mode === "sender" || mode === "settings") {
        setActiveTab(mode);
      }
    }).catch((err) => {
      console.error("Failed to load last active mode:", err);
    });
  }, []);

  // Save the mode whenever it changes
  const handleTabChange = (newTab: "receiver" | "sender" | "settings") => {
    setActiveTab(newTab);
    SaveLastActiveMode(newTab).catch((err) => {
      console.error("Failed to save last active mode:", err);
    });
  };

  return (
    <div className='flex flex-col h-screen w-screen bg-background text-foreground overflow-hidden'>
      <Topbar />
      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar */}
        <aside className="w-64 bg-card/50 border-r border-border flex flex-col p-4 space-y-2">
          <div className="mb-6 px-2">
            <h2 className="text-xs font-bold text-muted-foreground uppercase tracking-widest">Mode</h2>
          </div>

          <NavButton
            active={activeTab === "receiver"}
            onClick={() => handleTabChange("receiver")}
            icon={<Radio className="w-4 h-4 mr-2" />}
            label="Receiver"
          />
          <NavButton
            active={activeTab === "sender"}
            onClick={() => handleTabChange("sender")}
            icon={<Server className="w-4 h-4 mr-2" />}
            label="Sender"
          />

          <div className="mt-auto pt-4 border-t border-border">
            <NavButton
              active={activeTab === "settings"}
              onClick={() => handleTabChange("settings")}
              icon={<Settings className="w-4 h-4 mr-2" />}
              label="Settings"
            />
          </div>
        </aside>

        {/* Main Content */}
        {/*
        Note to self:
        Instead of using {activeTab === "receiver" && <ClientPage />} to conditionally render the tabs, which unmounts them whenever the condition is false, we use the method implemented below.
        This uses more memory, but since the app is not that complex overall, it does not really make a difference, I think.
        Why is this needed? -> To make the state of variables persist after switching tabs. Ex: Using GetServerConfig() on the ServerPage takes a few ms to load the value of the listenig port which is supposed to be displayed.
        While loading that value, we actually see the incorrect placeholder value of useState for a few ms everytime the tab is switched. Same for buttons, toggles etc.
        For now this implementation saves the pain of implementing proper libraries like "Zustand"
        */}
        <main className="flex-1 overflow-auto p-8 relative bg-background">
          <div className="max-w-5xl mx-auto h-full flex flex-col">
            <div style={{ display: activeTab === "receiver" ? "block" : "none" }}>
              <ClientPage />
            </div>
            <div style={{ display: activeTab === "sender" ? "block" : "none" }}>
              <ServerPage />
            </div>
            {/* For settings specifically, we want to unload the component when switching tabs, so that when settings are opened we always land on the General tab first and unsaved changes are discarded */}
            {activeTab === "settings" && <SettingsPage />}
          </div>
        </main>
      </div>
      <Toaster />
    </div >
  )
}

function NavButton({ active, onClick, icon, label }: { active: boolean, onClick: () => void, icon: React.ReactNode, label: string }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex items-center w-full px-3 py-2 rounded-md text-sm font-medium transition-all duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring",
        active
          ? "bg-primary text-primary-foreground shadow-sm"
          : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
      )}
    >
      {icon}
      {label}
    </button>
  )
}

export default App